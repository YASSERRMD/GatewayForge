package ratelimit

import (
	"fmt"
	"strings"
	"testing"
)

func TestTelemetryBoundedCardinalityAndPrometheusFormat(t *testing.T) {
	tel := NewTelemetry(4)

	// Record 50 distinct route IDs; cardinality MUST stay capped at 4 + __overflow__.
	for i := 0; i < 50; i++ {
		tel.Record(fmt.Sprintf("route-%02d", i), "allowed", "redis")
	}
	tel.Record("route-00", "denied", "redis")
	tel.Record("route-00", "allowed_fail_open", "fail_open")
	tel.Record("route-01", "denied_fail_closed", "fail_closed")

	snap := tel.Snapshot()
	if snap.TrackedRoutes != 4 {
		t.Fatalf("tracked routes=%d want 4", snap.TrackedRoutes)
	}
	if snap.TotalAllowed != 50 || snap.TotalDenied != 1 || snap.TotalFailOpen != 1 || snap.TotalFailClosed != 1 {
		t.Fatalf("unexpected totals: %+v", snap)
	}
	if !snap.Degraded {
		t.Fatal("expected degraded=true after fail_closed event")
	}

	prom := tel.FormatPrometheus()
	if !strings.Contains(prom, `route_id="__overflow__"`) {
		t.Fatalf("expected overflow bucket in Prometheus output:\n%s", prom)
	}
	if !strings.Contains(prom, `gatewayforge_ratelimit_decisions_total{route_id="route-00",outcome="denied",backend="redis"} 1`) {
		t.Fatalf("missing expected route-00 denied series:\n%s", prom)
	}
	// Verify recovery clears degraded flag when Redis succeeds again.
	tel.Record("route-00", "allowed", "redis")
	if tel.Snapshot().Degraded {
		t.Fatal("expected degraded=false after healthy redis decision")
	}
}
