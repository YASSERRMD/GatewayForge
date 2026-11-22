package proxy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	gwauth "github.com/YASSERRMD/GatewayForge/internal/gateway/auth"
	"github.com/YASSERRMD/GatewayForge/internal/gateway/balancer"
	gwcors "github.com/YASSERRMD/GatewayForge/internal/gateway/cors"
	"github.com/YASSERRMD/GatewayForge/internal/gateway/ratelimit"
	"github.com/YASSERRMD/GatewayForge/internal/gateway/resilience"
	"github.com/YASSERRMD/GatewayForge/internal/gateway/table"
	"github.com/YASSERRMD/GatewayForge/internal/shared/policy"
	"github.com/YASSERRMD/GatewayForge/internal/shared/snapshot"
)

// Engine executes reverse-proxy requests against the active immutable routing table.
type Engine struct {
	Holder         *table.Holder
	Transport      http.RoundTripper
	Policy         DestinationPolicy
	TrustedProxies TrustedProxyConfig
	Limits         ResourceLimits
	Selector       balancer.Selector
	Auth           *gwauth.Enforcer
	RateLimit      *ratelimit.Enforcer
	Health         *resilience.HealthTracker
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
		Limits:    DefaultResourceLimits(),
		Selector:  balancer.NewWeightedRoundRobin(),
	}
}

// ServeHTTP matches the request against the immutable snapshot and streams it to the selected upstream.
func (e *Engine) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r, corr := AttachCorrelation(w, r)

	tbl := e.Holder.Current()

	// Evaluate browser CORS preflight before auth on routes with a CORS policy.
	if gwcors.IsPreflight(r) {
		reqMethod := strings.ToUpper(strings.TrimSpace(r.Header.Get("Access-Control-Request-Method")))
		preMatch := tbl.Matcher.Match(r.Host, reqMethod, r.URL.Path)
		if preMatch.Matched {
			if corsCfg, hasCORS := gwcors.ResolveEffective(preMatch.Route.Policies); hasCORS {
				gwcors.HandlePreflight(w, r, corsCfg)
				return
			}
		}
	}

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

	// Apply CORS response headers for matched routes (including error responses).
	if corsCfg, hasCORS := gwcors.ResolveEffective(match.Route.Policies); hasCORS {
		gwcors.ApplyResponseHeaders(w, r, corsCfg)
	}

	// Enforce route/service/org consumer authentication & scopes before proxying.
	identity, stripHeaders, authErr := e.Auth.Enforce(r, tbl, match.Route)
	if authErr != nil {
		if errors.Is(authErr, gwauth.ErrForbidden) {
			WriteGatewayError(w, r, http.StatusForbidden, CodeForbidden, "insufficient scope for this route")
			return
		}
		WriteGatewayError(w, r, http.StatusUnauthorized, CodeUnauthorized, "authentication required")
		return
	}

	// Enforce distributed rate limits and UTC quotas after auth so consumer identity is known.
	if e.RateLimit != nil {
		rlRes := e.RateLimit.Enforce(r.Context(), r, tbl.OrgID, match.Route, identity.ConsumerID)
		if rlRes.EmitHeaders {
			ratelimit.WriteHeaders(w.Header(), rlRes.Decision)
		}
		if rlRes.DegradedOpen {
			w.Header().Set("X-RateLimit-Mode", "fail-open-degraded")
		}
		if !rlRes.Allowed {
			if rlRes.DeniedBy == "backend_unavailable" {
				WriteGatewayError(w, r, http.StatusServiceUnavailable, CodeServiceOverloaded, "rate limit backend unavailable")
				return
			}
			WriteGatewayError(w, r, http.StatusTooManyRequests, CodeRateLimited, "rate limit or quota exceeded")
			return
		}
	}

	pool, ok := tbl.Pools[match.Route.UpstreamPoolID]
	if !ok {
		WriteGatewayError(w, r, http.StatusServiceUnavailable, CodeUpstreamUnavailable, "configured upstream pool is unavailable")
		return
	}

	sel := e.Selector
	if sel == nil {
		sel = balancer.NewWeightedRoundRobin()
	}
	var healthFilter func(string) bool
	if e.Health != nil {
		healthFilter = func(targetID string) bool {
			return e.Health.IsHealthy(pool.ID, targetID)
		}
	}
	target, err := sel.Pick(pool, healthFilter)
	if err != nil {
		WriteGatewayError(w, r, http.StatusServiceUnavailable, CodeUpstreamUnavailable, "no healthy enabled targets available in upstream pool")
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

	limitedReq, cancel, ok := e.Limits.ApplyRequestLimits(w, r, match.Route, pool)
	if !ok {
		return
	}
	defer cancel()

	e.proxyStreaming(w, limitedReq, upstreamURL, corr, stripHeaders, pool.ID, target.ID, match.Route.Policies)
}

type retryRoundTripper struct {
	base      http.RoundTripper
	pol       policy.RetryPolicy
	bodyBytes []byte
}

func (rt *retryRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	var lastResp *http.Response
	var lastErr error
	for attempt := 1; attempt <= rt.pol.MaxAttempts; attempt++ {
		outReq := req.Clone(req.Context())
		if len(rt.bodyBytes) > 0 {
			outReq.Body = io.NopCloser(bytes.NewReader(rt.bodyBytes))
			outReq.ContentLength = int64(len(rt.bodyBytes))
		}
		resp, err := rt.base.RoundTrip(outReq)
		if err == nil && !resilience.IsRetryableOutcome(resp.StatusCode, nil, rt.pol) {
			return resp, nil
		}
		if err != nil && !resilience.IsRetryableOutcome(0, err, rt.pol) {
			return nil, err
		}
		if attempt == rt.pol.MaxAttempts {
			return resp, err
		}
		// Drain and close failed response body before retrying so connections can be reused cleanly.
		if resp != nil && resp.Body != nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			_ = resp.Body.Close()
		}
		lastResp, lastErr = resp, err
		delay := resilience.BackoffDelay(attempt, rt.pol, nil)
		if !resilience.SleepWithBudget(req.Context(), delay, nil) {
			if lastErr != nil {
				return nil, lastErr
			}
			return nil, req.Context().Err()
		}
	}
	return lastResp, lastErr
}

func (e *Engine) proxyStreaming(w http.ResponseWriter, r *http.Request, upstreamURL *url.URL, corr Correlation, stripHeaders []string, poolID, targetID string, policies []snapshot.AttachedPolicy) {
	transport := e.Transport
	if retryPol, hasRetry := resilience.ResolveRetryPolicy(policies); hasRetry && resilience.CanRetryMethod(r.Method, retryPol) {
		if bodyBytes, replayable, err := resilience.PrepareReplayBody(r, retryPol.MaxReplayBodyBytes); err == nil && replayable {
			transport = &retryRoundTripper{
				base:      e.Transport,
				pol:       retryPol,
				bodyBytes: bodyBytes,
			}
		}
	}

	rp := &httputil.ReverseProxy{
		Transport:     transport,
		FlushInterval: -1, // stream response chunks immediately without buffering
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = upstreamURL.Scheme
			pr.Out.URL.Host = upstreamURL.Host
			pr.Out.URL.Path = upstreamURL.Path
			pr.Out.URL.RawPath = ""
			pr.Out.URL.RawQuery = upstreamURL.RawQuery
			pr.Out.Host = upstreamURL.Host
			SanitizeOutboundHeaders(pr.In, pr.Out, e.TrustedProxies)
			for _, hdr := range stripHeaders {
				pr.Out.Header.Del(hdr)
			}
			ApplyCorrelationOutbound(pr.Out, corr)
		},
		ModifyResponse: func(resp *http.Response) error {
			StripHopByHop(resp.Header)
			if e.Health != nil {
				e.Health.RecordPassiveOutcome(poolID, targetID, resp.StatusCode, nil, resilience.DefaultPassiveConfig())
			}
			return nil
		},
		ErrorHandler: func(rw http.ResponseWriter, req *http.Request, proxyErr error) {
			if e.Health != nil {
				e.Health.RecordPassiveOutcome(poolID, targetID, 0, proxyErr, resilience.DefaultPassiveConfig())
			}
			var maxBytesErr *http.MaxBytesError
			switch {
			case errors.As(proxyErr, &maxBytesErr):
				WriteGatewayError(rw, req, http.StatusRequestEntityTooLarge, CodePayloadTooLarge, "request body exceeds route size limit")
			case errors.Is(proxyErr, context.DeadlineExceeded):
				WriteGatewayError(rw, req, http.StatusGatewayTimeout, CodeUpstreamTimeout, "upstream did not respond before deadline")
			case errors.Is(proxyErr, context.Canceled):
				// Client disconnected; write 499 status without upstream retry.
				rw.WriteHeader(499)
			case errors.Is(proxyErr, ErrBlockedDestination):
				WriteGatewayError(rw, req, http.StatusBadGateway, CodeUpstreamBlocked, "upstream destination blocked by policy")
			default:
				WriteGatewayError(rw, req, http.StatusBadGateway, CodeBadGateway, "upstream request failed")
			}
		},
	}
	rp.ServeHTTP(w, r)
}
