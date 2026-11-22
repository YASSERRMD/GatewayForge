package gwauth

import (
	"encoding/json"
	"testing"

	"github.com/YASSERRMD/GatewayForge/internal/shared/snapshot"
)

func TestResolveEffectiveAuthPrecedence(t *testing.T) {
	t.Run("no auth policies means public", func(t *testing.T) {
		eff := ResolveEffectiveAuth(nil)
		if !eff.Public || eff.RequireAPIKey || eff.RequireJWT {
			t.Fatalf("expected public, got %+v", eff)
		}
	})

	t.Run("route public override beats org api_key requirement", func(t *testing.T) {
		eff := ResolveEffectiveAuth([]snapshot.AttachedPolicy{
			{ID: "p-route", PolicyType: "api_key", ScopeType: "route", Config: json.RawMessage(`{"public":true}`)},
			{ID: "p-org", PolicyType: "api_key", ScopeType: "org", Config: json.RawMessage(`{"required_scopes":["base"]}`)},
		})
		if !eff.Public {
			t.Fatalf("expected route public override, got %+v", eff)
		}
	})

	t.Run("both api_key and jwt default to combinator=all and intersect scopes", func(t *testing.T) {
		eff := ResolveEffectiveAuth([]snapshot.AttachedPolicy{
			{ID: "p1", PolicyType: "api_key", ScopeType: "route", Config: json.RawMessage(`{"header_name":"X-Custom-Key","required_scopes":["orders:write"]}`)},
			{ID: "p2", PolicyType: "api_key", ScopeType: "org", Config: json.RawMessage(`{"header_name":"X-Api-Key","required_scopes":["org:access"]}`)},
			{ID: "p3", PolicyType: "jwt", ScopeType: "service", Config: json.RawMessage(`{"issuer":"https://idp","audience":"api","jwks_url":"http://jwks"}`)},
		})
		if eff.Public || !eff.RequireAPIKey || !eff.RequireJWT || eff.Combinator != "all" {
			t.Fatalf("unexpected combined policy: %+v", eff)
		}
		if eff.APIKeyConfig.HeaderName != "X-Custom-Key" {
			t.Fatalf("expected route-scoped header_name to win, got %q", eff.APIKeyConfig.HeaderName)
		}
		if len(eff.APIKeyConfig.RequiredScopes) != 2 {
			t.Fatalf("expected 2 intersected scopes, got %v", eff.APIKeyConfig.RequiredScopes)
		}
	})

	t.Run("combinator=any permits either api_key or jwt", func(t *testing.T) {
		eff := ResolveEffectiveAuth([]snapshot.AttachedPolicy{
			{ID: "p1", PolicyType: "api_key", ScopeType: "route", Config: json.RawMessage(`{"combinator":"any"}`)},
			{ID: "p2", PolicyType: "jwt", ScopeType: "route", Config: json.RawMessage(`{"issuer":"https://idp","jwks_url":"http://jwks"}`)},
		})
		if eff.Combinator != "any" {
			t.Fatalf("expected combinator=any, got %q", eff.Combinator)
		}
	})
}
