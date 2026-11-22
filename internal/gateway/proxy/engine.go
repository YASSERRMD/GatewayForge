package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httputil"
	"net/url"

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
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusMethodNotAllowed)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":    "method_not_allowed",
				"message": "HTTP method is not allowed for this route",
				"status":  http.StatusMethodNotAllowed,
			})
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":    "route_not_found",
			"message": "no configured route matches this request",
			"status":  http.StatusNotFound,
		})
		return
	}

	pool, ok := tbl.Pools[match.Route.UpstreamPoolID]
	if !ok {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":    "upstream_unavailable",
			"message": "configured upstream pool is unavailable",
			"status":  http.StatusServiceUnavailable,
		})
		return
	}

	target, ok := pickFirstEnabledTarget(pool)
	if !ok {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":    "upstream_unavailable",
			"message": "no enabled targets available in upstream pool",
			"status":  http.StatusServiceUnavailable,
		})
		return
	}

	targetURL, err := ValidateTargetURL(target.URL, e.Policy)
	if err != nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":    "upstream_blocked",
			"message": "upstream target blocked by destination policy",
			"status":  http.StatusBadGateway,
		})
		return
	}

	upstreamURL, err := BuildUpstreamURL(targetURL, r, match.Route.PathPrefix, match.Route.StripPrefix)
	if err != nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":    "request_invalid",
			"message": "request URI rejected by proxy safety rules",
			"status":  http.StatusBadRequest,
		})
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
			status := http.StatusBadGateway
			code := "bad_gateway"
			msg := "upstream request failed"
			if errors.Is(proxyErr, context.DeadlineExceeded) {
				status = http.StatusGatewayTimeout
				code = "upstream_timeout"
				msg = "upstream did not respond before deadline"
			} else if errors.Is(proxyErr, ErrBlockedDestination) {
				status = http.StatusBadGateway
				code = "upstream_blocked"
				msg = "upstream destination blocked by policy"
			}
			rw.Header().Set("Content-Type", "application/json; charset=utf-8")
			rw.WriteHeader(status)
			_ = json.NewEncoder(rw).Encode(map[string]any{
				"code":    code,
				"message": msg,
				"status":  status,
			})
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
