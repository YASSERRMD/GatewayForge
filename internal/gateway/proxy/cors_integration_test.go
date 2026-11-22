package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	gwauth "github.com/YASSERRMD/GatewayForge/internal/gateway/auth"
	gwcors "github.com/YASSERRMD/GatewayForge/internal/gateway/cors"
	"github.com/YASSERRMD/GatewayForge/internal/gateway/table"
	"github.com/YASSERRMD/GatewayForge/internal/shared/snapshot"
)

func TestEngineCORSPreflightBeforeAuthAndActualRequest(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	corsRaw, _ := json.Marshal(gwcors.PolicyConfig{
		AllowedOrigins:   []string{"https://studio.example.com"},
		AllowedMethods:   []string{"GET", "POST"},
		AllowedHeaders:   []string{"X-Api-Key", "Content-Type"},
		ExposedHeaders:   []string{"X-Request-Id"},
		AllowCredentials: true,
		MaxAgeSeconds:    120,
	})
	authRaw := json.RawMessage(`{"header_name":"X-Api-Key"}`)

	holder := table.NewHolder()
	_, err := holder.Apply(snapshot.Snapshot{
		SchemaVersion: snapshot.SchemaVersion,
		Version:       1,
		Pools: []snapshot.UpstreamPool{{
			ID:               "p1",
			ConnectTimeoutMS: 2000,
			ReadTimeoutMS:    5000,
			Targets:          []snapshot.UpstreamTarget{{ID: "t1", URL: upstream.URL, Weight: 100, Enabled: true}},
		}},
		Routes: []snapshot.Route{{
			ID:             "r1",
			Host:           "api.example.com",
			PathPrefix:     "/v1/orders",
			Methods:        []string{"GET", "POST"},
			UpstreamPoolID: "p1",
			Enabled:        true,
			Policies: []snapshot.AttachedPolicy{
				{ID: "pol-cors", PolicyType: "cors", ScopeType: "route", Config: corsRaw},
				{ID: "pol-auth", PolicyType: "api_key", ScopeType: "route", Config: authRaw},
			},
		}},
	}, false)
	if err != nil {
		t.Fatalf("apply snapshot: %v", err)
	}

	pol := DestinationPolicy{AllowLoopback: true}
	eng := NewEngine(holder, NewSafeTransport(DefaultTransportConfig(), pol), pol)
	eng.Auth = &gwauth.Enforcer{APIKeys: gwauth.NewAPIKeyAuthenticator(nil)}

	// 1. Preflight OPTIONS without API key must succeed with 204 and CORS headers (preflight runs before auth).
	preReq := httptest.NewRequest(http.MethodOptions, "http://api.example.com/v1/orders", nil)
	preReq.Header.Set("Origin", "https://studio.example.com")
	preReq.Header.Set("Access-Control-Request-Method", "POST")
	preReq.Header.Set("Access-Control-Request-Headers", "X-Api-Key")
	preRec := httptest.NewRecorder()
	eng.ServeHTTP(preRec, preReq)
	if preRec.Code != http.StatusNoContent {
		t.Fatalf("preflight status=%d want 204", preRec.Code)
	}
	if preRec.Header().Get("Access-Control-Allow-Origin") != "https://studio.example.com" {
		t.Fatalf("preflight ACAO=%q", preRec.Header().Get("Access-Control-Allow-Origin"))
	}

	// 2. Actual GET without API key must return 401 AND still include CORS response headers for approved origin.
	getReq := httptest.NewRequest(http.MethodGet, "http://api.example.com/v1/orders", nil)
	getReq.Header.Set("Origin", "https://studio.example.com")
	getRec := httptest.NewRecorder()
	eng.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated GET status=%d want 401", getRec.Code)
	}
	if getRec.Header().Get("Access-Control-Allow-Origin") != "https://studio.example.com" {
		t.Fatalf("401 ACAO=%q want https://studio.example.com", getRec.Header().Get("Access-Control-Allow-Origin"))
	}
}
