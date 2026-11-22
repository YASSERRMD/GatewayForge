package resilience

import (
	"context"
	"math"
	"time"

	"github.com/YASSERRMD/GatewayForge/internal/shared/policy"
)

// BackoffDelay computes bounded exponential backoff with symmetric jitter for 1-indexed retry attempt (attempt=1 is before 2nd try).
func BackoffDelay(attempt int, pol policy.RetryPolicy, randF func() float64) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if randF == nil {
		randF = cryptoRandFloat64
	}
	base := time.Duration(pol.BaseBackoffMS) * time.Millisecond
	maxD := time.Duration(pol.MaxBackoffMS) * time.Millisecond
	if base <= 0 {
		base = 50 * time.Millisecond
	}
	if maxD < base {
		maxD = base
	}

	mult := math.Pow(2, float64(attempt-1))
	raw := time.Duration(float64(base) * mult)
	if raw > maxD || raw <= 0 {
		raw = maxD
	}
	if pol.JitterFraction > 0 {
		delta := (randF()*2.0 - 1.0) * pol.JitterFraction
		jittered := time.Duration(float64(raw) * (1.0 + delta))
		if jittered < 0 {
			jittered = 0
		}
		if jittered > maxD {
			jittered = maxD
		}
		raw = jittered
	}
	return raw
}

// SleepWithBudget waits for delay unless ctx cancels or ctx's remaining deadline cannot accommodate the delay
// plus a small minimum execution buffer. Returns true if another attempt should proceed, false if budget is exhausted.
func SleepWithBudget(ctx context.Context, delay time.Duration, now func() time.Time) bool {
	if ctx.Err() != nil {
		return false
	}
	if now == nil {
		now = time.Now
	}
	if deadline, ok := ctx.Deadline(); ok {
		remaining := deadline.Sub(now())
		if remaining <= delay {
			return false
		}
	}
	if delay <= 0 {
		return true
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
