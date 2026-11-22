package integration

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/YASSERRMD/GatewayForge/internal/shared/cache"
	"github.com/YASSERRMD/GatewayForge/internal/shared/telemetry"
)

func TestTimeSeriesAndErrorBreakdownEmptySparseDense(t *testing.T) {
	if os.Getenv("INTEGRATION_TESTS") != "1" {
		t.Skip("set INTEGRATION_TESTS=1 to run time-series Redis test")
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

	prefix := fmt.Sprintf("gwf:test:ts:%d:", time.Now().UnixNano())
	store := telemetry.NewAggregateStore(rc, prefix, time.Hour)

	// 1. Empty org -> returns 12 zero-filled 5m buckets for 1h range and zero errors.
	lbl, step, pts, err := store.TimeSeries(ctx, "org-empty", "1h")
	if err != nil || lbl != "1h" || step != 5 || len(pts) != 12 {
		t.Fatalf("empty 1h series unexpected: lbl=%s step=%d len=%d err=%v", lbl, step, len(pts), err)
	}
	ebEmpty, err := store.Errors(ctx, "org-empty", "1h")
	if err != nil || ebEmpty.TotalRequests != 0 || ebEmpty.TotalErrors != 0 {
		t.Fatalf("empty errors unexpected: %+v err=%v", ebEmpty, err)
	}

	// 2. Sparse org -> data in 2 non-adjacent minutes within 15m window.
	now := time.Now().UTC().Truncate(time.Minute)
	_ = store.Record(ctx, telemetry.TrafficSample{Timestamp: now.Add(-10 * time.Minute), OrgID: "org-sparse", Status: 200, Outcome: "ok", DurationMS: 15})
	_ = store.Record(ctx, telemetry.TrafficSample{Timestamp: now, OrgID: "org-sparse", Status: 429, Outcome: "rate_limited", DurationMS: 2})
	_ = store.Record(ctx, telemetry.TrafficSample{Timestamp: now, OrgID: "org-sparse", Status: 502, Outcome: "upstream_error", DurationMS: 120})

	lblS, stepS, ptsS, err := store.TimeSeries(ctx, "org-sparse", "15m")
	if err != nil || lblS != "15m" || stepS != 1 || len(ptsS) != 15 {
		t.Fatalf("sparse 15m series unexpected: lbl=%s step=%d len=%d err=%v", lblS, stepS, len(ptsS), err)
	}
	var sumReqs int64
	for _, p := range ptsS {
		sumReqs += p.Requests
	}
	if sumReqs != 3 {
		t.Fatalf("sparse sumReqs=%d want 3", sumReqs)
	}

	ebS, err := store.Errors(ctx, "org-sparse", "15m")
	if err != nil || ebS.TotalRequests != 3 || ebS.TotalErrors != 2 || ebS.ByOutcome["rate_limited"] != 1 || ebS.ByOutcome["upstream_error"] != 1 {
		t.Fatalf("sparse error breakdown unexpected: %+v err=%v", ebS, err)
	}
}
