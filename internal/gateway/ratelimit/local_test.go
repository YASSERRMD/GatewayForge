package ratelimit

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/YASSERRMD/GatewayForge/internal/shared/policy"
)

func TestLocalLimiterBurstRefillAndClockSkew(t *testing.T) {
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	lim := NewLocalLimiter(clock, 100)
	pol := policy.RateLimitPolicy{
		KeyBy:             policy.KeyByConsumer,
		RequestsPerWindow: 2, // 2 req / 10s -> 1 token every 5s
		WindowSeconds:     10,
		Burst:             3,
	}

	// Consume full burst of 3 immediately at t=0.
	for i := 0; i < 3; i++ {
		d, err := lim.Allow(context.Background(), "k1", pol)
		if err != nil || !d.Allowed {
			t.Fatalf("req %d expected allow, got %+v err=%v", i+1, d, err)
		}
		wantRem := int64(2 - i)
		if d.Remaining != wantRem {
			t.Fatalf("req %d remaining=%d want %d", i+1, d.Remaining, wantRem)
		}
	}

	// 4th request at t=0 must be denied with RetryAfter == 5s.
	d, err := lim.Allow(context.Background(), "k1", pol)
	if err != nil || d.Allowed {
		t.Fatalf("expected deny on 4th request, got %+v err=%v", d, err)
	}
	if d.RetryAfter != 5*time.Second {
		t.Fatalf("RetryAfter=%v want 5s", d.RetryAfter)
	}

	// Clock moves backwards (skew): must NOT mint tokens.
	now = now.Add(-2 * time.Second)
	d, _ = lim.Allow(context.Background(), "k1", pol)
	if d.Allowed {
		t.Fatal("negative clock skew must not refill tokens")
	}

	// Advance 5s past initial t=0 -> exactly 1 token refilled.
	now = time.Date(2026, 11, 22, 10, 0, 5, 0, time.UTC)
	d, _ = lim.Allow(context.Background(), "k1", pol)
	if !d.Allowed || d.Remaining != 0 {
		t.Fatalf("expected 1 refilled token at +5s, got %+v", d)
	}

	// Advance +30s -> capped at Burst (3), never overflows capacity.
	now = now.Add(30 * time.Second)
	d, _ = lim.Allow(context.Background(), "k1", pol)
	if !d.Allowed || d.Remaining != 2 {
		t.Fatalf("expected capped burst remaining=2 after long idle, got %+v", d)
	}
}

func TestLocalLimiterConcurrencyAndBoundedCapacity(t *testing.T) {
	lim := NewLocalLimiter(nil, 4)
	pol := policy.RateLimitPolicy{RequestsPerWindow: 5, WindowSeconds: 60, Burst: 5}

	var wg sync.WaitGroup
	var mu sync.Mutex
	allowed := 0
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, err := lim.Allow(context.Background(), "shared-key", pol)
			if err == nil && d.Allowed {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if allowed != 5 {
		t.Fatalf("allowed=%d want exactly 5 under concurrency", allowed)
	}

	// Bounded capacity check: inserting 10 keys keeps map <= 4.
	for i := 0; i < 10; i++ {
		_, _ = lim.Allow(context.Background(), "k-"+string(rune('a'+i)), pol)
	}
	lim.mu.Lock()
	size := len(lim.buckets)
	lim.mu.Unlock()
	if size > 4 {
		t.Fatalf("bucket table exceeded maxBuckets=4: got %d", size)
	}
}
