package proxy

import (
	"context"
	"errors"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/YASSERRMD/GatewayForge/internal/gateway/table"
	"github.com/YASSERRMD/GatewayForge/internal/shared/snapshot"
)

// Engine executes reverse-proxy requests against the active immutable routing table.
type Engine struct {
	Holder         *table.Holder
	Transport      http.RoundTripper
	Policy         DestinationPolicy
	TrustedProxies TrustedProxyConfig
}

// NewEngine creates a streaming reverse-proxy Engine backed by holder.
func NewEngine(holder *table.Holder, transport http.RoundTripper, pol DestinationPolicy) *Engine {
	if holder == nil {
		holder = table.NewHolder()
	}
	if transport == nil {
		transport = http.DefaultTransport
	}
	return &Engine{
		Holder:    holder,
		Transport: transport,
		Policy:    pol,
	}
}

// ServeHTTP matches the request against the immutable snapshot and streams it to the selected upstream.
func (e *Engine) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r, corr := AttachCorrelation(w, r)

	tbl := e.Holder.Current()
	match := tbl.Matcher.Match(r.Host, r.Method, r.URL.Path)
	if !match.Matched {
		if match.MethodMismatch {
			if len(match.AllowedMethods) > 0 {
				w.Header().Set("Allow", strings.Join(match.AllowedMethods, ", "))
			}
			WriteGatewayError(w, r, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "HTTP method is not allowed for this route")
			return
		}
		WriteGatewayError(w, r, http.StatusNotFound, CodeRouteNotFound, "no configured route matches this request")
		return
	}

	pool, ok := tbl.Pools[match.Route.UpstreamPoolID]
	if !ok {
		WriteGatewayError(w, r, http.StatusServiceUnavailable, CodeUpstreamUnavailable, "configured upstream pool is unavailable")
		return
	}

	target, ok := pickFirstEnabledTarget(pool)
	if !ok {
		WriteGatewayError(w, r, http.StatusServiceUnavailable, CodeUpstreamUnavailable, "no enabled targets available in upstream pool")
		return
	}

	targetURL, err := ValidateTargetURL(target.URL, e.Policy)
	if err != nil {
		WriteGatewayError(w, r, http.StatusBadGateway, CodeUpstreamBlocked, "upstream target blocked by destination policy")
		return
	}

	upstreamURL, err := BuildUpstreamURL(targetURL, r, match.Route.PathPrefix, match.Route.StripPrefix)
	if err != nil {
		WriteGatewayError(w, r, http.StatusBadRequest, CodeRequestInvalid, "request URI rejected by proxy safety rules")
		return
	}

	e.proxyStreaming(w, r, upstreamURL, corr)
}

func (e *Engine) proxyStreaming(w http.ResponseWriter, r *http.Request, upstreamURL *url.URL, corr Correlation) {
	rp := &httputil.ReverseProxy{
		Transport:     e.Transport,
		FlushInterval: -1, // stream response chunks immediately without buffering
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = upstreamURL.Scheme
			pr.Out.URL.Host = upstreamURL.Host
			pr.Out.URL.Path = upstreamURL.Path
			pr.Out.URL.RawPath = ""
			pr.Out.URL.RawQuery = upstreamURL.RawQuery
			pr.Out.Host = upstreamURL.Host
			SanitizeOutboundHeaders(pr.In, pr.Out, e.TrustedProxies)
			ApplyCorrelationOutbound(pr.Out, corr)
		},
		ModifyResponse: func(resp *http.Response) error {
			StripHopByHop(resp.Header)
			return nil
		},
		ErrorHandler: func(rw http.ResponseWriter, req *http.Request, proxyErr error) {
			switch {
			case errors.Is(proxyErr, context.DeadlineExceeded):
				WriteGatewayError(rw, req, http.StatusGatewayTimeout, CodeUpstreamTimeout, "upstream did not respond before deadline")
			case errors.Is(proxyErr, ErrBlockedDestination):
				WriteGatewayError(rw, req, http.StatusBadGateway, CodeUpstreamBlocked, "upstream destination blocked by policy")
			default:
				WriteGatewayError(rw, req, http.StatusBadGateway, CodeBadGateway, "upstream request failed")
			}
		},
	}
	rp.ServeHTTP(w, r)
}

func pickFirstEnabledTarget(pool snapshot.UpstreamPool) (snapshot.UpstreamTarget, bool) {
	for _, t := range pool.Targets {
		if t.Enabled {
			return t, true
		}
	}
	return snapshot.UpstreamTarget{}, false
}
