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
	"github.com/YASSERRMD/GatewayForge/internal/shared/netsec"
)

func TestReleaseRollbackAfterRouteFailureRestoresSnapshotAndAudits(t *testing.T) {
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
	pubH := &api.PublicationHandler{Store: st, Sessions: sessMgr, Audit: auditRec, TargetPolicy: targetPol}

	router := api.NewRouter(api.RouterOptions{
		MountV1: func(v1 chi.Router) {
			authH.MountAuthRoutes(v1)
			svcH.MountServiceRoutes(v1)
			upH.MountUpstreamRoutes(v1)
			rtH.MountRouteRoutes(v1)
			pubH.MountPublicationRoutes(v1)
		},
	})
	ts := httptest.NewServer(router)
	defer ts.Close()

	_, _ = doOAJSON(t, http.MethodPost, ts.URL+"/api/v1/auth/bootstrap", map[string]any{
		"email": "rb-admin@acme.test", "password": "SuperSecurePass!2026",
		"display_name": "RB Admin", "org_name": "RB Org", "org_slug": "rb-org",
	}, nil, "")
	loginRes, loginBody := doOAJSON(t, http.MethodPost, ts.URL+"/api/v1/auth/login", map[string]any{
		"email": "rb-admin@acme.test", "password": "SuperSecurePass!2026",
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

	// Setup Service + Pool + Route v1
	_, svcBody := doOAJSON(t, http.MethodPost, ts.URL+"/api/v1/orgs/"+orgID+"/services", map[string]any{
		"slug": "orders", "name": "Orders", "description": "",
	}, cookies, loginOut.CSRFToken)
	var svc struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(svcBody, &svc)

	_, poolBody := doOAJSON(t, http.MethodPost, ts.URL+"/api/v1/orgs/"+orgID+"/upstreams", map[string]any{
		"name": "orders-pool", "destination_class": "mock_local",
		"targets": []map[string]any{{"url": "http://mock-upstream:8089", "weight": 100}},
	}, cookies, loginOut.CSRFToken)
	var up struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(poolBody, &up)

	_, rtBody := doOAJSON(t, http.MethodPost, ts.URL+"/api/v1/orgs/"+orgID+"/routes", map[string]any{
		"service_id": svc.ID, "upstream_pool_id": up.ID,
		"name": "orders-get", "host": "api.rb.local", "path_prefix": "/v1/orders",
		"methods": []string{"GET"}, "priority": 100,
	}, cookies, loginOut.CSRFToken)
	var rt struct {
		ID       string `json:"id"`
		Revision int64  `json:"revision"`
	}
	_ = json.Unmarshal(rtBody, &rt)

	// Publish v1 (good config)
	p1Res, p1Body := doOAJSON(t, http.MethodPost, ts.URL+"/api/v1/orgs/"+orgID+"/publications", map[string]any{
		"notes": "good v1", "activate": true,
	}, cookies, loginOut.CSRFToken)
	if p1Res.StatusCode != http.StatusCreated {
		t.Fatalf("publish v1 failed: %d %s", p1Res.StatusCode, string(p1Body))
	}
	var pub1 struct {
		Version  int    `json:"version"`
		Checksum string `json:"checksum"`
	}
	_ = json.Unmarshal(p1Body, &pub1)

	// Break route in draft (disable route) and publish v2
	_, _ = doOAJSON(t, http.MethodPatch, ts.URL+"/api/v1/orgs/"+orgID+"/routes/"+rt.ID, map[string]any{
		"enabled": false, "revision": rt.Revision,
	}, cookies, loginOut.CSRFToken)

	p2Res, p2Body := doOAJSON(t, http.MethodPost, ts.URL+"/api/v1/orgs/"+orgID+"/publications", map[string]any{
		"notes": "broken v2", "activate": true,
	}, cookies, loginOut.CSRFToken)
	if p2Res.StatusCode != http.StatusCreated {
		t.Fatalf("publish v2 failed: %d %s", p2Res.StatusCode, string(p2Body))
	}
	var pub2 struct {
		ActivationRev int64 `json:"activation_revision"`
	}
	_ = json.Unmarshal(p2Body, &pub2)

	// Rollback to immutable v1
	rbRes, rbBody := doOAJSON(t, http.MethodPost, ts.URL+"/api/v1/orgs/"+orgID+"/publications/1/activate", map[string]any{
		"expected_activation_revision": pub2.ActivationRev,
		"reason":                       "route failure rollback",
	}, cookies, loginOut.CSRFToken)
	if rbRes.StatusCode != http.StatusOK {
		t.Fatalf("rollback failed: %d %s", rbRes.StatusCode, string(rbBody))
	}

	var activeVer int
	var activeChecksum string
	if err := pool.QueryRow(context.Background(), `
		SELECT p.version, p.checksum_sha256
		FROM active_publications ap
		JOIN publications p ON p.id = ap.publication_id
		WHERE ap.org_id = $1
	`, orgID).Scan(&activeVer, &activeChecksum); err != nil {
		t.Fatalf("query active pub: %v", err)
	}
	if activeVer != 1 || activeChecksum != pub1.Checksum {
		t.Fatalf("expected active v1 (%s), got v%d (%s)", pub1.Checksum, activeVer, activeChecksum)
	}

	var rollbackActCount, auditRollbackCount int
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM publication_activations WHERE org_id = $1 AND is_rollback = TRUE`, orgID).Scan(&rollbackActCount)
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_events WHERE org_id = $1 AND action = 'publication.rollback'`, orgID).Scan(&auditRollbackCount)
	if rollbackActCount != 1 || auditRollbackCount != 1 {
		t.Fatalf("expected 1 rollback activation and 1 audit event, got act=%d audit=%d", rollbackActCount, auditRollbackCount)
	}
}
