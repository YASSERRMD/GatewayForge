package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/YASSERRMD/GatewayForge/internal/control/api"
	"github.com/YASSERRMD/GatewayForge/internal/control/auth"
	"github.com/YASSERRMD/GatewayForge/internal/control/store"
	"github.com/YASSERRMD/GatewayForge/internal/gateway/table"
	"github.com/YASSERRMD/GatewayForge/internal/shared/netsec"
	"github.com/YASSERRMD/GatewayForge/internal/shared/snapshot"
)

func TestFailedRolloutPreservesLastKnownGoodAndSurfacesErrors(t *testing.T) {
	pool := openIntegrationDB(t)
	defer pool.Close()
	resetAndMigrateDB(t, pool)
	st := store.New(pool)

	hasher := newTestArgonHasher()
	bootstrapSvc := auth.NewBootstrapService(pool, "test", hasher)
	sessMgr := auth.NewSessionManager(pool, hasher, time.Hour, false)
	auditRec := api.NewAuditRecorder(st)
	targetPol := netsec.DefaultTargetPolicy(true)

	authH := &api.AuthHandler{Bootstrap: bootstrapSvc, Sessions: sessMgr}
	svcH := &api.ServiceHandler{Store: st, Sessions: sessMgr, Audit: auditRec}
	upH := &api.UpstreamHandler{Store: st, Sessions: sessMgr, Audit: auditRec, TargetPolicy: targetPol}
	rtH := &api.RouteHandler{Store: st, Sessions: sessMgr, Audit: auditRec}
	polH := &api.PolicyHandler{Store: st, Sessions: sessMgr, Audit: auditRec}
	pubH := &api.PublicationHandler{Store: st, Sessions: sessMgr, Audit: auditRec, TargetPolicy: targetPol}

	router := api.NewRouter(api.RouterOptions{
		MountV1: func(v1 chi.Router) {
			authH.MountAuthRoutes(v1)
			svcH.MountServiceRoutes(v1)
			upH.MountUpstreamRoutes(v1)
			rtH.MountRouteRoutes(v1)
			polH.MountPolicyRoutes(v1)
			pubH.MountPublicationRoutes(v1)
		},
	})
	ts := httptest.NewServer(router)
	defer ts.Close()

	_, _ = doOAJSON(t, http.MethodPost, ts.URL+"/api/v1/auth/bootstrap", map[string]any{
		"email": "lkg-admin@acme.test", "password": "SuperSecurePass!2026",
		"display_name": "LKG Admin", "org_name": "LKG Org", "org_slug": "lkg-org",
	}, nil, "")
	loginRes, loginBody := doOAJSON(t, http.MethodPost, ts.URL+"/api/v1/auth/login", map[string]any{
		"email": "lkg-admin@acme.test", "password": "SuperSecurePass!2026",
	}, nil, "")
	cookies := loginRes.Cookies()
	var loginOut struct {
		CSRFToken string `json:"csrf_token"`
	}
	_ = json.Unmarshal(loginBody, &loginOut)
	_, meBody := doOAJSON(t, http.MethodGet, ts.URL+"/api/v1/auth/me", nil, cookies, "")
	var meOut struct {
		Memberships []struct {
			OrgID string `json:"org_id"`
		} `json:"memberships"`
	}
	_ = json.Unmarshal(meBody, &meOut)
	orgID := meOut.Memberships[0].OrgID

	// 1. Publish valid v1
	_, svcBody := doOAJSON(t, http.MethodPost, ts.URL+"/api/v1/orgs/"+orgID+"/services", map[string]any{
		"slug": "catalog", "name": "Catalog", "description": "",
	}, cookies, loginOut.CSRFToken)
	var svc struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(svcBody, &svc)

	_, poolBody := doOAJSON(t, http.MethodPost, ts.URL+"/api/v1/orgs/"+orgID+"/upstreams", map[string]any{
		"name": "catalog-pool", "destination_class": "mock_local",
		"targets": []map[string]any{{"url": "http://mock-upstream:8089", "weight": 100}},
	}, cookies, loginOut.CSRFToken)
	var up struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(poolBody, &up)

	_, _ = doOAJSON(t, http.MethodPost, ts.URL+"/api/v1/orgs/"+orgID+"/routes", map[string]any{
		"service_id": svc.ID, "upstream_pool_id": up.ID,
		"name": "catalog-get", "host": "api.lkg.local", "path_prefix": "/v1/catalog",
		"methods": []string{"GET"}, "priority": 100,
	}, cookies, loginOut.CSRFToken)

	p1Res, p1Body := doOAJSON(t, http.MethodPost, ts.URL+"/api/v1/orgs/"+orgID+"/publications", map[string]any{
		"notes": "good v1", "activate": true,
	}, cookies, loginOut.CSRFToken)
	if p1Res.StatusCode != http.StatusCreated {
		t.Fatalf("publish v1 failed: %d %s", p1Res.StatusCode, string(p1Body))
	}
	var pub1 struct {
		Checksum string `json:"checksum"`
	}
	_ = json.Unmarshal(p1Body, &pub1)

	// 2. Introduce invalid policy (critical + fail_open) -> publish MUST fail 422 & keep v1 active
	_, _ = doOAJSON(t, http.MethodPost, ts.URL+"/api/v1/orgs/"+orgID+"/policies", map[string]any{
		"name":        "unsafe-rl",
		"policy_type": "rate_limit",
		"config": map[string]any{
			"key_by":              "consumer",
			"requests_per_window": 10,
			"window_seconds":      60,
			"security_class":      "critical",
			"fail_mode":           "fail_open",
		},
	}, cookies, loginOut.CSRFToken)

	p2Res, p2Body := doOAJSON(t, http.MethodPost, ts.URL+"/api/v1/orgs/"+orgID+"/publications", map[string]any{
		"notes": "invalid v2", "activate": true,
	}, cookies, loginOut.CSRFToken)
	if p2Res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 invalid_draft, got %d: %s", p2Res.StatusCode, string(p2Body))
	}

	var activeVer int
	_ = pool.QueryRow(context.Background(), `
		SELECT p.version FROM active_publications ap JOIN publications p ON p.id = ap.publication_id WHERE ap.org_id = $1
	`, orgID).Scan(&activeVer)
	if activeVer != 1 {
		t.Fatalf("expected LKG v1 preserved after invalid publish, got v%d", activeVer)
	}

	// 3. Gateway table store rejects tampered snapshot checksum and preserves LKG v1
	var snapRaw json.RawMessage
	_ = pool.QueryRow(context.Background(), `SELECT snapshot_json FROM publications WHERE org_id = $1 AND version = 1`, orgID).Scan(&snapRaw)
	var validSnap snapshot.Snapshot
	if err := json.Unmarshal(snapRaw, &validSnap); err != nil {
		t.Fatalf("unmarshal v1 snapshot: %v", err)
	}

	tbl := table.NewHolder()
	if _, err := tbl.Apply(validSnap, true); err != nil {
		t.Fatalf("apply valid LKG v1 failed: %v", err)
	}

	tampered := validSnap
	tampered.Version = 2
	tampered.Checksum = "0000000000000000000000000000000000000000000000000000000000000000"
	if _, err := tbl.Apply(tampered, true); err == nil {
		t.Fatal("expected gateway table holder to reject tampered snapshot checksum")
	}
	if tbl.LastError() == "" {
		t.Fatal("expected gateway table holder to record non-empty LastError after failed apply")
	}

	current := tbl.Current()
	if current == nil || current.Version != 1 || current.Checksum != pub1.Checksum {
		t.Fatalf("expected LKG v1 (%s) preserved after tampered apply, got %+v", pub1.Checksum, current)
	}
}
