package ratelimit

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/YASSERRMD/GatewayForge/internal/shared/policy"
)

// Decision is the deterministic outcome of evaluating a rate limit or quota check.
type Decision struct {
	Allowed    bool
	Limit      int64
	Remaining  int64
	ResetAfter time.Duration
	RetryAfter time.Duration
	Window     time.Duration
	Backend    string // "local" | "redis" | "fail_open" | "fail_closed"
}

// Limiter evaluates rate-limit policies for a derived bucket key.
type Limiter interface {
	Allow(ctx context.Context, key string, pol policy.RateLimitPolicy) (Decision, error)
}

type localBucket struct {
	tokens    float64
	lastRefil time.Time
	expiresAt time.Time
}

// LocalLimiter is a deterministic, concurrency-safe in-memory token-bucket limiter
// with an injectable clock and bounded key table size.
type LocalLimiter struct {
	now        func() time.Time
	maxBuckets int

	mu      sync.Mutex
	buckets map[string]*localBucket
}

// NewLocalLimiter constructs a LocalLimiter. If now is nil, time.Now().UTC() is used.
func NewLocalLimiter(now func() time.Time, maxBuckets int) *LocalLimiter {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if maxBuckets <= 0 {
		maxBuckets = 10000
	}
	return &LocalLimiter{
		now:        now,
		maxBuckets: maxBuckets,
		buckets:    make(map[string]*localBucket),
	}
}

// Allow evaluates a single request against pol for key using continuous token-bucket refill.
func (l *LocalLimiter) Allow(_ context.Context, key string, pol policy.RateLimitPolicy) (Decision, error) {
	pol.Normalize()
	if err := pol.Validate(); err != nil {
		return Decision{}, err
	}

	now := l.now()
	window := time.Duration(pol.WindowSeconds) * time.Second
	capacity := float64(pol.Burst)
	refillRate := float64(pol.RequestsPerWindow) / float64(pol.WindowSeconds) // tokens per second
	ttl := window * 2
	if ttl < 2*time.Second {
		ttl = 2 * time.Second
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.buckets[key]
	if !ok {
		if len(l.buckets) >= l.maxBuckets {
			l.evictExpiredLocked(now)
		}
		b = &localBucket{
			tokens:    capacity,
			lastRefil: now,
			expiresAt: now.Add(ttl),
		}
		l.buckets[key] = b
	} else {
		elapsed := now.Sub(b.lastRefil).Seconds()
		if elapsed > 0 {
			b.tokens = math.Min(capacity, b.tokens+elapsed*refillRate)
			b.lastRefil = now
		}
		b.expiresAt = now.Add(ttl)
	}

	if b.tokens >= 1.0 {
		b.tokens -= 1.0
		remaining := int64(math.Floor(b.tokens))
		missing := capacity - b.tokens
		resetSec := missing / refillRate
		return Decision{
			Allowed:    true,
			Limit:      pol.Burst,
			Remaining:  remaining,
			ResetAfter: secondsToDuration(resetSec),
			RetryAfter: 0,
			Window:     window,
			Backend:    "local",
		}, nil
	}

	needed := 1.0 - b.tokens
	retrySec := needed / refillRate
	resetSec := (capacity - b.tokens) / refillRate
	return Decision{
		Allowed:    false,
		Limit:      pol.Burst,
		Remaining:  0,
		ResetAfter: secondsToDuration(resetSec),
		RetryAfter: secondsToDuration(retrySec),
		Window:     window,
		Backend:    "local",
	}, nil
}

func (l *LocalLimiter) evictExpiredLocked(now time.Time) {
	for k, b := range l.buckets {
		if now.After(b.expiresAt) {
			delete(l.buckets, k)
		}
	}
	// If still at capacity, remove one oldest-expiring entry to stay strictly bounded.
	if len(l.buckets) >= l.maxBuckets {
		var oldestKey string
		var oldestTime time.Time
		first := true
		for k, b := range l.buckets {
			if first || b.expiresAt.Before(oldestTime) {
				oldestKey = k
				oldestTime = b.expiresAt
				first = false
			}
		}
		if oldestKey != "" {
			delete(l.buckets, oldestKey)
		}
	}
}

func secondsToDuration(sec float64) time.Duration {
	if sec <= 0 {
		return 0
	}
	return time.Duration(math.Ceil(sec * float64(time.Second)))
}
