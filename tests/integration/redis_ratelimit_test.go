package integration

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/YASSERRMD/GatewayForge/internal/gateway/ratelimit"
	"github.com/YASSERRMD/GatewayForge/internal/shared/cache"
	"github.com/YASSERRMD/GatewayForge/internal/shared/policy"
)

func TestRedisDistributedLimiterConcurrencyAndIsolation(t *testing.T) {
	if os.Getenv("INTEGRATION_TESTS") != "1" {
		t.Skip("set INTEGRATION_TESTS=1 to run Redis distributed limiter integration test")
	}
	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "127.0.0.1:6389"
	}
	redisPw := os.Getenv("REDIS_PASSWORD")
	if redisPw == "" {
		redisPw = "gatewayforge_local_redis_pw"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	rc, err := cache.Connect(ctx, redisAddr, redisPw)
	if err != nil {
		t.Fatalf("connect redis: %v", err)
	}
	defer func() { _ = rc.Close() }()

	lim := ratelimit.NewRedisLimiter(rc, 500*time.Millisecond)
	pol := policy.RateLimitPolicy{
		KeyBy:             policy.KeyByConsumer,
		RequestsPerWindow: 10,
		WindowSeconds:     60,
		Burst:             10,
	}

	keyA := fmt.Sprintf("gwf:test:rl:orgA:route1:cons1:%d", time.Now().UnixNano())
	keyB := fmt.Sprintf("gwf:test:rl:orgB:route1:cons1:%d", time.Now().UnixNano())
	defer func() { _ = rc.Del(context.Background(), keyA, keyB).Err() }()

	// Fire 40 concurrent checks on keyA: atomically EXACTLY 10 must be allowed, 30 denied.
	var allowed atomic.Int64
	var denied atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, err := lim.Allow(ctx, keyA, pol)
			if err != nil {
				t.Errorf("redis allow error: %v", err)
				return
			}
			if d.Allowed {
				allowed.Add(1)
			} else {
				if d.RetryAfter <= 0 {
					t.Errorf("denied decision missing positive RetryAfter: %+v", d)
				}
				denied.Add(1)
			}
		}()
	}
	wg.Wait()

	if allowed.Load() != 10 || denied.Load() != 30 {
		t.Fatalf("expected exactly 10 allowed / 30 denied, got allowed=%d denied=%d", allowed.Load(), denied.Load())
	}

	// Org B isolated key must still have full capacity.
	dB, err := lim.Allow(ctx, keyB, pol)
	if err != nil || !dB.Allowed || dB.Remaining != 9 {
		t.Fatalf("expected isolated orgB key to succeed with remaining=9, got %+v err=%v", dB, err)
	}
}
