package metrics

import (
	"strings"
	"testing"
	"time"
)

func TestPrometheusExpositionStableNames(t *testing.T) {
	reg := NewRegistry(16, 16)
	reg.SetActiveConfigVersion(7)
	reg.IncInFlight()
	reg.ObserveRequest("rt-orders", "svc-orders", "GET", 200, "ok", 18*time.Millisecond)
	reg.ObserveRequest("rt-orders", "svc-orders", "GET", 401, "auth_denied", 2*time.Millisecond)
	reg.ObserveRequest("rt-orders", "svc-orders", "POST", 504, "timeout", 1500*time.Millisecond)
	reg.DecInFlight()

	out := reg.FormatPrometheus()
	wantSnippets := []string{
		"gatewayforge_requests_in_flight 0",
		"gatewayforge_active_config_version 7",
		`gatewayforge_requests_total{route_id="rt-orders",service_id="svc-orders",method="GET",status_class="2xx",outcome="ok"} 1`,
		`gatewayforge_request_duration_seconds_count{route_id="rt-orders",service_id="svc-orders"} 3`,
		`gatewayforge_auth_failures_total{route_id="rt-orders",status="401"} 1`,
		`gatewayforge_upstream_failures_total{route_id="rt-orders",service_id="svc-orders",status="504"} 1`,
	}
	for _, s := range wantSnippets {
		if !strings.Contains(out, s) {
			t.Fatalf("missing expected metric line %q in:\n%s", s, out)
		}
	}
}
