package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/YASSERRMD/GatewayForge/internal/control/api"
	"github.com/YASSERRMD/GatewayForge/internal/control/auth"
	"github.com/YASSERRMD/GatewayForge/internal/control/store"
	"github.com/YASSERRMD/GatewayForge/internal/gateway"
	"github.com/YASSERRMD/GatewayForge/internal/shared/config"
	"github.com/YASSERRMD/GatewayForge/internal/shared/netsec"
	"github.com/YASSERRMD/GatewayForge/internal/shared/snapshot"
)

func TestUpstreamHealthDiagnosticsRoleVisibilityAndSafety(t *testing.T) {
	pool := openIntegrationDB(t)
	resetAndMigrateDB(t, pool)

	hasher := newTestArgonHasher()
	bootstrap := auth.NewBootstrapService(pool, config.EnvTest, hasher)
	sessions := auth.NewSessionManager(pool, hasher, time.Hour, false)
	st := store.New(pool)
	auditRec := api.NewAuditRecorder(st)
	targetPol := netsec.DefaultTargetPolicy(true)
	targetPol.AllowLoopback = true

	authH := &api.AuthHandler{Bootstrap: bootstrap, Sessions: sessions}
	memH := &api.MembershipHandler{Store: st, Sessions: sessions, Hasher: hasher, Audit: auditRec}
	upH := &api.UpstreamHandler{Store: st, Sessions: sessions, Audit: auditRec, TargetPolicy: targetPol}

	srv := httptest.NewServer(api.NewRouter(api.RouterOptions{
		Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
		MountV1: func(v1 chi.Router) {
			authH.MountAuthRoutes(v1)
			memH.MountMembershipRoutes(v1)
			upH.MountUpstreamRoutes(v1)
		},
	}))
	defer srv.Close()

	client := &http.Client{Timeout: 5 * time.Second}
	doReq := func(method, path string, body any, cookie *http.Cookie, csrf string) (*http.Response, []byte) {
		var rdr io.Reader
		if body != nil {
			raw, _ := json.Marshal(body)
			rdr = bytes.NewReader(raw)
		}
		req, _ := http.NewRequest(method, srv.URL+path, rdr)
		req.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			req.AddCookie(cookie)
		}
		if csrf != "" {
			req.Header.Set("X-CSRF-Token", csrf)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return resp, b
	}

	// Bootstrap admin + login.
	resp, b := doReq(http.MethodPost, "/api/v1/auth/bootstrap", map[string]string{
		"email": "admin@diag.test", "password": "VeryStrongPassword!99", "display_name": "Admin", "org_name": "DiagOrg", "org_slug": "diag-org",
	}, nil, "")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("bootstrap=%d %s", resp.StatusCode, b)
	}
	var boot struct {
		OrgID string `json:"org_id"`
	}
	_ = json.Unmarshal(b, &boot)

	login := func(email, pw string) (*http.Cookie, string) {
		r, rb := doReq(http.MethodPost, "/api/v1/auth/login", map[string]string{"email": email, "password": pw}, nil, "")
		if r.StatusCode != http.StatusOK {
			t.Fatalf("login %s=%d %s", email, r.StatusCode, rb)
		}
		var out struct {
			CSRFToken string `json:"csrf_token"`
		}
		_ = json.Unmarshal(rb, &out)
		for _, c := range r.Cookies() {
			if c.Name == auth.SessionCookieName {
				return c, out.CSRFToken
			}
		}
		t.Fatalf("no session cookie for %s", email)
		return nil, ""
	}

	adminCookie, adminCSRF := login("admin@diag.test", "VeryStrongPassword!99")

	// Create viewer member in the same org.
	resp, b = doReq(http.MethodPost, fmt.Sprintf("/api/v1/orgs/%s/members", boot.OrgID), map[string]string{
		"email": "viewer@diag.test", "password": "ViewerStrongPassword!99", "display_name": "Viewer", "role": "viewer",
	}, adminCookie, adminCSRF)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create viewer=%d %s", resp.StatusCode, b)
	}
	viewerCookie, _ := login("viewer@diag.test", "ViewerStrongPassword!99")

	// Create upstream pool with internal target address.
	resp, b = doReq(http.MethodPost, fmt.Sprintf("/api/v1/orgs/%s/upstreams", boot.OrgID), map[string]any{
		"name": "internal-orders", "destination_class": "mock_local",
		"targets": []map[string]any{{"url": "http://127.0.0.1:18089", "weight": 100}},
	}, adminCookie, adminCSRF)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create pool=%d %s", resp.StatusCode, b)
	}

	// 1. Viewer sees aggregated status/counts ONLY and NEVER sees internal host/IP address!
	resp, b = doReq(http.MethodGet, fmt.Sprintf("/api/v1/orgs/%s/upstreams/health", boot.OrgID), nil, viewerCookie, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("viewer health=%d %s", resp.StatusCode, b)
	}
	if strings.Contains(string(b), "127.0.0.1") || strings.Contains(string(b), "18089") || strings.Contains(string(b), `"targets"`) {
		t.Fatalf("viewer response leaked internal target address/details: %s", b)
	}
	if !strings.Contains(string(b), `"visibility":"aggregated_viewer"`) || !strings.Contains(string(b), `"enabled_targets":1`) {
		t.Fatalf("viewer missing expected aggregated fields: %s", b)
	}

	// 2. Admin sees sanitized operator target details.
	resp, b = doReq(http.MethodGet, fmt.Sprintf("/api/v1/orgs/%s/upstreams/health", boot.OrgID), nil, adminCookie, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin health=%d %s", resp.StatusCode, b)
	}
	if !strings.Contains(string(b), `"visibility":"operator"`) || !strings.Contains(string(b), `"targets"`) {
		t.Fatalf("admin missing expected operator target details: %s", b)
	}

	// 3. Gateway /health/upstreams emits safe aggregated pool/target IDs WITHOUT raw internal URLs.
	gw := gateway.NewServer(gateway.ServerConfig{Addr: "127.0.0.1:0", AllowMockUpstreams: true, AllowLoopback: true})
	_, _ = gw.TableHolder().Apply(snapshot.Snapshot{
		SchemaVersion: snapshot.SchemaVersion,
		Version:       1,
		Pools: []snapshot.UpstreamPool{{
			ID: "pool-secret-host", ConnectTimeoutMS: 2000, ReadTimeoutMS: 5000,
			Targets: []snapshot.UpstreamTarget{{ID: "t1", URL: "http://10.99.88.77:9000", Weight: 100, Enabled: true}},
		}},
	}, false)
	rec := httptest.NewRecorder()
	gw.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health/upstreams", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("gateway /health/upstreams=%d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "10.99.88.77") {
		t.Fatalf("gateway /health/upstreams leaked internal IP: %s", rec.Body.String())
	}
}
