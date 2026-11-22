package resilience

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/YASSERRMD/GatewayForge/internal/shared/policy"
	"github.com/YASSERRMD/GatewayForge/internal/shared/snapshot"
)

// ResolveRetryPolicy returns the highest-precedence valid retry policy attached to a route.
func ResolveRetryPolicy(policies []snapshot.AttachedPolicy) (policy.RetryPolicy, bool) {
	for _, ap := range policies {
		if ap.PolicyType != "retry" {
			continue
		}
		p, err := policy.ParseRetryPolicy(ap.Config)
		if err != nil {
			continue
		}
		return p, true
	}
	return policy.RetryPolicy{}, false
}

// CanRetryMethod enforces the strict idempotent retry invariant at runtime.
// Non-idempotent methods (POST, PATCH, CONNECT) are ALWAYS forbidden from automatic retry.
func CanRetryMethod(method string, pol policy.RetryPolicy) bool {
	upper := strings.ToUpper(strings.TrimSpace(method))
	if upper == http.MethodPost || upper == http.MethodPatch || upper == http.MethodConnect {
		return false
	}
	if pol.MaxAttempts <= 1 {
		return false
	}
	for _, m := range pol.RetryableMethods {
		if strings.ToUpper(strings.TrimSpace(m)) == upper {
			return true
		}
	}
	return false
}

// IsRetryableOutcome reports whether an upstream response status or transport error is eligible for retry.
func IsRetryableOutcome(statusCode int, err error, pol policy.RetryPolicy) bool {
	if err != nil {
		// Never retry when the client canceled the request or the overall deadline has already expired.
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return false
		}
		return true
	}
	// Never retry 2xx, 3xx, or 4xx responses.
	if statusCode < 500 {
		return false
	}
	for _, code := range pol.RetryableStatuses {
		if statusCode == code {
			return true
		}
	}
	return false
}

// PrepareReplayBody buffers up to maxBytes of r.Body so an idempotent request can be safely replayed.
// If the body exceeds maxBytes, it restores a streaming reader (prefix + remainder) and returns replayable=false
// so the gateway forwards the request ONCE without retrying or buffering unbounded memory.
func PrepareReplayBody(r *http.Request, maxBytes int64) (bodyBytes []byte, replayable bool, err error) {
	if r.Body == nil || r.Body == http.NoBody {
		return nil, true, nil
	}
	if maxBytes <= 0 {
		return nil, false, nil
	}
	limited := io.LimitReader(r.Body, maxBytes+1)
	buf, err := io.ReadAll(limited)
	if err != nil {
		return nil, false, err
	}
	if int64(len(buf)) > maxBytes {
		// Exceeds replay buffer cap: stitch read prefix back onto unread remainder for single-pass streaming.
		orig := r.Body
		r.Body = struct {
			io.Reader
			io.Closer
		}{
			Reader: io.MultiReader(bytes.NewReader(buf), orig),
			Closer: orig,
		}
		return nil, false, nil
	}
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(buf))
	r.ContentLength = int64(len(buf))
	return buf, true, nil
}
