package proxy

import (
	"context"
	"net/http"
	"time"

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

// ResolveRequestTimeout computes the effective request deadline from route and pool configuration.
func (l ResourceLimits) ResolveRequestTimeout(route snapshot.Route, pool snapshot.UpstreamPool) time.Duration {
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

	timeout := l.ResolveRequestTimeout(route, pool)
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	return r.WithContext(ctx), cancel, true
}
