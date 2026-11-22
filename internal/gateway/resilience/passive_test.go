package resilience

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/YASSERRMD/GatewayForge/internal/shared/snapshot"
)

func TestPassiveFailureTrackingIgnores4xxAndClientCancel(t *testing.T) {
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	ht := NewHealthTracker(DefaultActiveProbeConfig(), nil, nil)
	ht.SetClock(func() time.Time { return now })
	ht.SyncPools(map[string]snapshot.UpstreamPool{
		"pool-1": {ID: "pool-1", Targets: []snapshot.UpstreamTarget{{ID: "t1", URL: "http://t1.local", Weight: 100, Enabled: true}}},
	})
	cfg := PassiveConfig{ConsecutiveFailures: 3, Cooldown: 10 * time.Second}

	// Business 4xx responses (400, 401, 403, 404, 422, 429) MUST NOT mark target unhealthy.
	for _, code := range []int{400, 401, 403, 404, 409, 422, 429} {
		ht.RecordPassiveOutcome("pool-1", "t1", code, nil, cfg)
	}
	if !ht.IsHealthy("pool-1", "t1") {
		t.Fatal("business 4xx responses must not eject healthy target")
	}

	// Client cancellations MUST NOT poison target health.
	for i := 0; i < 5; i++ {
		ht.RecordPassiveOutcome("pool-1", "t1", 0, context.Canceled, cfg)
	}
	if !ht.IsHealthy("pool-1", "t1") {
		t.Fatal("client context.Canceled must not eject healthy target")
	}

	// 2 transport errors + 1 business 404 resets failure streak!
	ht.RecordPassiveOutcome("pool-1", "t1", 0, errors.New("dial timeout"), cfg)
	ht.RecordPassiveOutcome("pool-1", "t1", http.StatusBadGateway, nil, cfg)
	ht.RecordPassiveOutcome("pool-1", "t1", http.StatusNotFound, nil, cfg)
	if !ht.IsHealthy("pool-1", "t1") {
		t.Fatal("404 should have reset failure streak")
	}

	// 3 consecutive transport/503/504 failures eject target for 10s cooldown.
	ht.RecordPassiveOutcome("pool-1", "t1", 0, context.DeadlineExceeded, cfg)
	ht.RecordPassiveOutcome("pool-1", "t1", http.StatusServiceUnavailable, nil, cfg)
	ht.RecordPassiveOutcome("pool-1", "t1", http.StatusGatewayTimeout, nil, cfg)
	if ht.IsHealthy("pool-1", "t1") {
		t.Fatal("target should be unhealthy after 3 consecutive upstream failures")
	}

	// After 9s still ejected; at +10s cooldown expires allowing trial traffic -> 200 recovers it.
	now = now.Add(9 * time.Second)
	if ht.IsHealthy("pool-1", "t1") {
		t.Fatal("target should still be ejected before cooldown elapses")
	}
	now = now.Add(1 * time.Second)
	if !ht.IsHealthy("pool-1", "t1") {
		t.Fatal("target should be eligible for trial traffic after cooldown")
	}
	ht.RecordPassiveOutcome("pool-1", "t1", http.StatusOK, nil, cfg)
	rec, _ := ht.RecordSnapshot("pool-1", "t1")
	if rec.Status != StatusHealthy {
		t.Fatalf("expected StatusHealthy after successful trial request, got %s", rec.Status)
	}
}
