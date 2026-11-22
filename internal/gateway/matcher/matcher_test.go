package matcher

import (
	"testing"

	"github.com/YASSERRMD/GatewayForge/internal/shared/snapshot"
)

func TestStrictRouteMatcherPrecedenceAndBoundaries(t *testing.T) {
	m := Compile([]snapshot.Route{
		{
			ID: "r-root", Host: "api.local", PathPrefix: "/api", Methods: []string{"GET"},
			Priority: 100, Enabled: true,
		},
		{
			ID: "r-orders", Host: "api.local", PathPrefix: "/api/orders", Methods: []string{"GET", "POST"},
			Priority: 100, Enabled: true,
		},
		{
			ID: "r-high-prio", Host: "api.local", PathPrefix: "/api", Methods: []string{"DELETE"},
			Priority: 200, Enabled: true,
		},
		{
			ID: "r-disabled", Host: "api.local", PathPrefix: "/disabled", Methods: []string{"GET"},
			Priority: 500, Enabled: false,
		},
		{
			ID: "r-tie-b", Host: "tie.local", PathPrefix: "/v1", Methods: []string{"GET"},
			Priority: 50, Enabled: true,
		},
		{
			ID: "r-tie-a", Host: "tie.local", PathPrefix: "/v1", Methods: []string{"GET"},
			Priority: 50, Enabled: true,
		},
	})

	t.Run("longer prefix wins at equal priority", func(t *testing.T) {
		res := m.Match("API.LOCAL:18081", "GET", "/api/orders/42")
		if !res.Matched || res.Route.ID != "r-orders" {
			t.Fatalf("expected r-orders, got %+v", res)
		}
	})

	t.Run("segment boundary prevents /api matching /apiv2", func(t *testing.T) {
		res := m.Match("api.local", "GET", "/apiv2/orders")
		if res.Matched || res.MethodMismatch {
			t.Fatalf("expected no match for /apiv2/orders, got %+v", res)
		}
	})

	t.Run("method mismatch reports allowed methods", func(t *testing.T) {
		res := m.Match("api.local", "PUT", "/api/orders")
		if res.Matched || !res.MethodMismatch || len(res.AllowedMethods) == 0 {
			t.Fatalf("expected method mismatch with allowed methods, got %+v", res)
		}
	})

	t.Run("disabled route is never matched", func(t *testing.T) {
		res := m.Match("api.local", "GET", "/disabled")
		if res.Matched || res.MethodMismatch {
			t.Fatalf("disabled route must not match, got %+v", res)
		}
	})

	t.Run("unknown host is rejected", func(t *testing.T) {
		res := m.Match("evil.example", "GET", "/api/orders")
		if res.Matched || res.MethodMismatch {
			t.Fatalf("unknown host must not match, got %+v", res)
		}
	})

	t.Run("deterministic tie-breaking by route ID ASC", func(t *testing.T) {
		res := m.Match("tie.local", "GET", "/v1/item")
		if !res.Matched || res.Route.ID != "r-tie-a" {
			t.Fatalf("expected r-tie-a by deterministic tie-break, got %+v", res)
		}
	})
}
