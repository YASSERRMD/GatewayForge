package proxy

import (
	"context"
	"net/http"
	"time"

	"github.com/YASSERRMD/GatewayForge/internal/gateway/resilience"
	"github.com/YASSERRMD/GatewayForge/internal/shared/snapshot"
)

// ResourceLimits defines default safety caps enforced on every proxied request.
type ResourceLimits struct {
	DefaultMaxBodyBytes   int64
	MaxHeaderCount        int
	DefaultRequestTimeout time.Duration
}

// DefaultResourceLimits returns production-safe defaults.
func DefaultResourceLimits() ResourceLimits {
	return ResourceLimits{
		DefaultMaxBodyBytes:   10 << 20, // 10 MiB
		MaxHeaderCount:        100,
		DefaultRequestTimeout: 30 * time.Second,
	}
}

// ResolveBodyLimit returns the effective body size cap in bytes for a route.
func (l ResourceLimits) ResolveBodyLimit(route snapshot.Route) int64 {
	if route.MaxBodyBytes > 0 {
		return route.MaxBodyBytes
	}
	if l.DefaultMaxBodyBytes > 0 {
		return l.DefaultMaxBodyBytes
	}
	return 10 << 20
}

// ResolveRequestTimeout computes the effective total request deadline from timeout policy, route, and pool config.
func (l ResourceLimits) ResolveRequestTimeout(route snapshot.Route, pool snapshot.UpstreamPool) time.Duration {
	if tp, ok := resilience.ResolveTimeoutPolicy(route.Policies); ok && tp.TotalTimeoutMS > 0 {
		return time.Duration(tp.TotalTimeoutMS) * time.Millisecond
	}
	if route.RequestTimeoutMS > 0 {
		return time.Duration(route.RequestTimeoutMS) * time.Millisecond
	}
	if pool.ReadTimeoutMS > 0 {
		return time.Duration(pool.ReadTimeoutMS) * time.Millisecond
	}
	if l.DefaultRequestTimeout > 0 {
		return l.DefaultRequestTimeout
	}
	return 30 * time.Second
}

// ResolveConnectTimeout computes the effective upstream TCP connect timeout from timeout policy or pool config.
func (l ResourceLimits) ResolveConnectTimeout(route snapshot.Route, pool snapshot.UpstreamPool) time.Duration {
	if tp, ok := resilience.ResolveTimeoutPolicy(route.Policies); ok && tp.ConnectTimeoutMS > 0 {
		return time.Duration(tp.ConnectTimeoutMS) * time.Millisecond
	}
	if pool.ConnectTimeoutMS > 0 {
		return time.Duration(pool.ConnectTimeoutMS) * time.Millisecond
	}
	return 3 * time.Second
}

// CheckHeaderCount rejects requests carrying an excessive number of header fields.
func (l ResourceLimits) CheckHeaderCount(h http.Header) bool {
	maxHeaders := l.MaxHeaderCount
	if maxHeaders <= 0 {
		maxHeaders = 100
	}
	count := 0
	for _, vals := range h {
		count += len(vals)
		if count > maxHeaders {
			return false
		}
	}
	return true
}

// ApplyRequestLimits applies header-count checks, body size capping, and deadline context.
// Returns (wrappedReq, cancel, ok). When ok is false, an error response has already been written.
func (l ResourceLimits) ApplyRequestLimits(
	w http.ResponseWriter,
	r *http.Request,
	route snapshot.Route,
	pool snapshot.UpstreamPool,
) (*http.Request, context.CancelFunc, bool) {
	if !l.CheckHeaderCount(r.Header) {
		WriteGatewayError(w, r, http.StatusRequestHeaderFieldsTooLarge, CodeRequestInvalid, "too many request headers")
		return nil, func() {}, false
	}

	maxBytes := l.ResolveBodyLimit(route)
	if r.ContentLength > maxBytes {
		WriteGatewayError(w, r, http.StatusRequestEntityTooLarge, CodePayloadTooLarge, "request body exceeds route size limit")
		return nil, func() {}, false
	}
	if r.Body != nil {
		r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	}

	connectTimeout := l.ResolveConnectTimeout(route, pool)
	totalTimeout := l.ResolveRequestTimeout(route, pool)
	baseCtx := resilience.WithConnectTimeout(r.Context(), connectTimeout)
	ctx, cancel := context.WithTimeout(baseCtx, totalTimeout)
	return r.WithContext(ctx), cancel, true
}
