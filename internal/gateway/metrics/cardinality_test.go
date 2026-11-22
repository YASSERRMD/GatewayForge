package metrics

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/YASSERRMD/GatewayForge/internal/shared/redact"
)

func TestCardinalityControlsRejectHighCardinalityAndSecrets(t *testing.T) {
	reg := NewRegistry(8, 4)

	// 1. Feed 500 synthetic route IDs + 500 secret/path-like values.
	for i := 0; i < 500; i++ {
		reg.ObserveRequest(fmt.Sprintf("route-%04d", i), fmt.Sprintf("svc-%04d", i), "GET", 200, "ok", time.Millisecond)
	}
	rawKey := "gwf_deadbeef1234.0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	rawJWT := "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJ1MSJ9.c2lnbmF0dXJlMTIzNDU2"
	reg.ObserveRequest(rawKey, "svc-1", "GET", 401, "auth_denied", time.Millisecond)
	reg.ObserveRequest(rawJWT, "/api/v1/users/99999?token=abc", "GET", 404, "client_error", time.Millisecond)

	routes, services := reg.TrackedCardinality()
	if routes > 8 || services > 4 {
		t.Fatalf("cardinality cap violated: routes=%d (max 8), services=%d (max 4)", routes, services)
	}

	prom := reg.FormatPrometheus()
	if leaks := redact.FindLeaks(prom, rawKey, rawJWT, "/api/v1/users/99999"); len(leaks) > 0 {
		t.Fatalf("Prometheus output leaked secret/path: %v", leaks)
	}
	if !strings.Contains(prom, `route_id="__overflow__"`) {
		t.Fatalf("expected overflow bucket in Prometheus scrape:\n%s", prom)
	}

	// 2. ValidateLabel policy tests.
	for _, badName := range []string{"api_key", "token", "client_ip", "raw_path", "request_id", "unknown_label"} {
		if err := ValidateLabel(badName, "val"); err == nil {
			t.Fatalf("expected ValidateLabel to reject label %q", badName)
		}
	}
	if err := ValidateLabel("route_id", rawKey); err == nil {
		t.Fatal("expected ValidateLabel to reject API key value")
	}
	if err := ValidateLabel("route_id", "/users/123"); err == nil {
		t.Fatal("expected ValidateLabel to reject raw path value")
	}
	if err := ValidateLabel("route_id", "rt-orders"); err != nil {
		t.Fatalf("expected valid route_id label to pass: %v", err)
	}
}
