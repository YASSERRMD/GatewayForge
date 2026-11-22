package resilience

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/YASSERRMD/GatewayForge/internal/shared/snapshot"
)

func TestActiveHealthProbeTransitionsAndDisabledInvariant(t *testing.T) {
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	var failTarget1 atomic.Bool
	var disabledProbed atomic.Int32

	cfg := ActiveProbeConfig{
		Interval:           5 * time.Second,
		Timeout:            500 * time.Millisecond,
		JitterFraction:     0.2,
		HealthyThreshold:   2,
		UnhealthyThreshold: 3,
		Path:               "/health",
	}

	ht := NewHealthTracker(cfg, nil, func(_ context.Context, targetURL, _ string) error {
		if targetURL == "http://disabled.local" {
			disabledProbed.Add(1)
			return nil
		}
		if failTarget1.Load() {
			return errors.New("connection refused")
		}
		return nil
	})
	ht.SetClock(func() time.Time { return now })

	ht.SyncPools(map[string]snapshot.UpstreamPool{
		"pool-1": {
			ID: "pool-1",
			Targets: []snapshot.UpstreamTarget{
				{ID: "t1", URL: "http://t1.local", Weight: 100, Enabled: true},
				{ID: "t-disabled", URL: "http://disabled.local", Weight: 100, Enabled: false},
			},
		},
	})

	if !ht.IsHealthy("pool-1", "t1") {
		t.Fatal("t1 should start healthy")
	}
	if ht.IsHealthy("pool-1", "t-disabled") {
		t.Fatal("disabled target must never report healthy")
	}

	// Inject 3 consecutive probe failures -> t1 flips to unhealthy on 3rd failure.
	failTarget1.Store(true)
	ht.ProbeAll(context.Background())
	ht.ProbeAll(context.Background())
	if !ht.IsHealthy("pool-1", "t1") {
		t.Fatal("t1 should still be healthy after 2 failures (threshold=3)")
	}
	ht.ProbeAll(context.Background())
	if ht.IsHealthy("pool-1", "t1") {
		t.Fatal("t1 should transition to unhealthy after 3 consecutive failures")
	}
	if disabledProbed.Load() != 0 {
		t.Fatalf("administratively disabled target must never be probed, probed=%d", disabledProbed.Load())
	}

	// Recover after 2 consecutive healthy probes (HealthyThreshold=2).
	failTarget1.Store(false)
	now = now.Add(5 * time.Second)
	ht.ProbeAll(context.Background())
	if ht.IsHealthy("pool-1", "t1") {
		t.Fatal("t1 should stay unhealthy after only 1 success (threshold=2)")
	}
	now = now.Add(5 * time.Second)
	ht.ProbeAll(context.Background())
	if !ht.IsHealthy("pool-1", "t1") {
		t.Fatal("t1 should recover to healthy after 2 consecutive successes")
	}

	// Verify jittered interval stays strictly within [4s, 6s] for Interval=5s, Jitter=0.2.
	for i := 0; i < 20; i++ {
		d := ht.NextJitteredInterval()
		if d < 4*time.Second || d > 6*time.Second {
			t.Fatalf("jittered interval %v out of expected [4s, 6s] range", d)
		}
	}
}
