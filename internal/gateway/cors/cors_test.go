package cors

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/YASSERRMD/GatewayForge/internal/shared/snapshot"
)

func TestValidateConfigRejectsWildcardWithCredentials(t *testing.T) {
	err := ValidateConfig(PolicyConfig{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET"},
		AllowCredentials: true,
	})
	if err == nil {
		t.Fatal("expected wildcard origin with allow_credentials=true to be rejected")
	}

	err = ValidateConfig(PolicyConfig{
		AllowedOrigins:   []string{"https://app.example.com"},
		AllowedMethods:   []string{"GET"},
		AllowedHeaders:   []string{"*"},
		AllowCredentials: true,
	})
	if err == nil {
		t.Fatal("expected wildcard header with allow_credentials=true to be rejected")
	}

	if err := ValidateConfig(PolicyConfig{
		AllowedOrigins:   []string{"https://app.example.com"},
		AllowedMethods:   []string{"GET", "POST"},
		AllowedHeaders:   []string{"Authorization", "X-Api-Key"},
		AllowCredentials: true,
		MaxAgeSeconds:    600,
	}); err != nil {
		t.Fatalf("expected valid credentialed CORS config to pass: %v", err)
	}
}

func TestHandlePreflightAllowedAndDenied(t *testing.T) {
	cfg := PolicyConfig{
		AllowedOrigins:   []string{"https://console.example.com"},
		AllowedMethods:   []string{"GET", "POST"},
		AllowedHeaders:   []string{"Authorization", "Content-Type", "X-Api-Key"},
		ExposedHeaders:   []string{"X-Request-Id"},
		AllowCredentials: true,
		MaxAgeSeconds:    300,
	}

	t.Run("allowed preflight returns 204 and exact headers", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodOptions, "http://api.example.com/v1/orders", nil)
		req.Header.Set("Origin", "https://console.example.com")
		req.Header.Set("Access-Control-Request-Method", "POST")
		req.Header.Set("Access-Control-Request-Headers", "content-type, x-api-key")
		rec := httptest.NewRecorder()

		HandlePreflight(rec, req, cfg)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status=%d want 204", rec.Code)
		}
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://console.example.com" {
			t.Fatalf("ACAO=%q want https://console.example.com", got)
		}
		if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
			t.Fatalf("ACAC=%q want true", got)
		}
		if got := rec.Header().Get("Access-Control-Max-Age"); got != "300" {
			t.Fatalf("Max-Age=%q want 300", got)
		}
		vary := strings.Join(rec.Header().Values("Vary"), ", ")
		if !strings.Contains(vary, "Origin") || !strings.Contains(vary, "Access-Control-Request-Method") {
			t.Fatalf("missing Vary headers: %q", vary)
		}
	})

	t.Run("disallowed origin denied with 403 and no ACAO", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodOptions, "http://api.example.com/v1/orders", nil)
		req.Header.Set("Origin", "https://evil.example.com")
		req.Header.Set("Access-Control-Request-Method", "GET")
		rec := httptest.NewRecorder()

		HandlePreflight(rec, req, cfg)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status=%d want 403", rec.Code)
		}
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("must not emit ACAO for disallowed origin, got %q", got)
		}
	})

	t.Run("disallowed method denied with 403", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodOptions, "http://api.example.com/v1/orders", nil)
		req.Header.Set("Origin", "https://console.example.com")
		req.Header.Set("Access-Control-Request-Method", "DELETE")
		rec := httptest.NewRecorder()

		HandlePreflight(rec, req, cfg)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status=%d want 403", rec.Code)
		}
	})

	t.Run("disallowed header denied with 403", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodOptions, "http://api.example.com/v1/orders", nil)
		req.Header.Set("Origin", "https://console.example.com")
		req.Header.Set("Access-Control-Request-Method", "POST")
		req.Header.Set("Access-Control-Request-Headers", "X-Forbidden-Header")
		rec := httptest.NewRecorder()

		HandlePreflight(rec, req, cfg)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status=%d want 403", rec.Code)
		}
	})

	t.Run("wildcard with credentials fails closed at runtime", func(t *testing.T) {
		bad := PolicyConfig{
			AllowedOrigins:   []string{"*"},
			AllowedMethods:   []string{"GET"},
			AllowCredentials: true,
		}
		req := httptest.NewRequest(http.MethodOptions, "http://api.example.com/v1/orders", nil)
		req.Header.Set("Origin", "https://console.example.com")
		req.Header.Set("Access-Control-Request-Method", "GET")
		rec := httptest.NewRecorder()

		HandlePreflight(rec, req, bad)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status=%d want 403 for wildcard+credentials", rec.Code)
		}
		if rec.Header().Get("Access-Control-Allow-Origin") != "" || rec.Header().Get("Access-Control-Allow-Credentials") != "" {
			t.Fatalf("must never emit CORS allow headers for wildcard+credentials")
		}
	})
}

func TestApplyResponseHeadersAndPrecedence(t *testing.T) {
	routeCfg, _ := json.Marshal(PolicyConfig{
		AllowedOrigins: []string{"https://route.example.com"},
		AllowedMethods: []string{"GET"},
		ExposedHeaders: []string{"X-Request-Id", "X-RateLimit-Remaining"},
	})
	orgCfg, _ := json.Marshal(PolicyConfig{
		AllowedOrigins: []string{"https://org.example.com"},
		AllowedMethods: []string{"GET"},
	})
	cfg, ok := ResolveEffective([]snapshot.AttachedPolicy{
		{PolicyType: "cors", ScopeType: "route", Config: routeCfg},
		{PolicyType: "cors", ScopeType: "org", Config: orgCfg},
	})
	if !ok {
		t.Fatal("expected effective CORS policy")
	}

	req := httptest.NewRequest(http.MethodGet, "http://api.example.com/v1/items", nil)
	req.Header.Set("Origin", "https://route.example.com")
	rec := httptest.NewRecorder()
	ApplyResponseHeaders(rec, req, cfg)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://route.example.com" {
		t.Fatalf("ACAO=%q want route origin", got)
	}
	if got := rec.Header().Get("Access-Control-Expose-Headers"); !strings.Contains(got, "X-Request-Id") {
		t.Fatalf("Expose-Headers=%q", got)
	}
	if got := rec.Header().Get("Vary"); !strings.Contains(got, "Origin") {
		t.Fatalf("Vary=%q want Origin", got)
	}

	// Org-only origin must NOT be echoed because route policy took precedence.
	req2 := httptest.NewRequest(http.MethodGet, "http://api.example.com/v1/items", nil)
	req2.Header.Set("Origin", "https://org.example.com")
	rec2 := httptest.NewRecorder()
	ApplyResponseHeaders(rec2, req2, cfg)
	if got := rec2.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("unexpected ACAO=%q for overridden org origin", got)
	}
}
