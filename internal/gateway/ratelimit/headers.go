package ratelimit

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"
)

// CeilSeconds converts a duration to deterministic ceiling whole seconds (minimum minSec when d > 0).
func CeilSeconds(d time.Duration, minSec int64) int64 {
	if d <= 0 {
		return minSec
	}
	secs := int64(math.Ceil(d.Seconds()))
	if secs < minSec {
		return minSec
	}
	return secs
}

// WriteHeaders writes standard RateLimit, RateLimit-Policy, X-RateLimit-*, and (when denied) Retry-After headers.
func WriteHeaders(w http.Header, d Decision) {
	if w == nil || d.Limit <= 0 {
		return
	}
	remaining := d.Remaining
	if remaining < 0 {
		remaining = 0
	}
	resetSec := CeilSeconds(d.ResetAfter, 0)
	windowSec := CeilSeconds(d.Window, 1)

	w.Set("RateLimit-Policy", fmt.Sprintf("%d;w=%d", d.Limit, windowSec))
	w.Set("RateLimit", fmt.Sprintf("limit=%d, remaining=%d, reset=%d", d.Limit, remaining, resetSec))
	w.Set("X-RateLimit-Limit", strconv.FormatInt(d.Limit, 10))
	w.Set("X-RateLimit-Remaining", strconv.FormatInt(remaining, 10))
	w.Set("X-RateLimit-Reset", strconv.FormatInt(resetSec, 10))

	if !d.Allowed {
		retrySec := CeilSeconds(d.RetryAfter, 1)
		w.Set("Retry-After", strconv.FormatInt(retrySec, 10))
	}
}
