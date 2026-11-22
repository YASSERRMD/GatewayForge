package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
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

func doOAJSON(t *testing.T, method, url string, payload any, cookies []*http.Cookie, csrf string) (*http.Response, []byte) {
	t.Helper()
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	return resp, raw
}

func TestOpenAPITransactionalImportAndPartialFailureRollback(t *testing.T) {
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
	upH := &api.UpstreamHandler{Store: st, Sessions: sessMgr, Audit: auditRec, TargetPolicy: targetPol}
	rtH := &api.RouteHandler{Store: st, Sessions: sessMgr, Audit: auditRec}
	oaH := &api.OpenAPIHandler{Store: st, Sessions: sessMgr, Audit: auditRec}

	router := api.NewRouter(api.RouterOptions{
		MountV1: func(v1 chi.Router) {
			authH.MountAuthRoutes(v1)
			upH.MountUpstreamRoutes(v1)
			rtH.MountRouteRoutes(v1)
			oaH.MountOpenAPIRoutes(v1)
		},
	})
	ts := httptest.NewServer(router)
	defer ts.Close()

	// Bootstrap admin + org
	res, body := doOAJSON(t, http.MethodPost, ts.URL+"/api/v1/auth/bootstrap", map[string]any{
		"email":        "oa-admin@acme.test",
		"password":     "SuperSecurePass!2026",
		"display_name": "OA Admin",
		"org_name":     "OpenAPI Org",
		"org_slug":     "openapi-org",
	}, nil, "")
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("bootstrap failed: %d %s", res.StatusCode, string(body))
	}
	// Login
	loginRes, loginBody := doOAJSON(t, http.MethodPost, ts.URL+"/api/v1/auth/login", map[string]any{
		"email":    "oa-admin@acme.test",
		"password": "SuperSecurePass!2026",
	}, nil, "")
	if loginRes.StatusCode != http.StatusOK {
		t.Fatalf("login failed: %d %s", loginRes.StatusCode, string(loginBody))
	}
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
	if len(meOut.Memberships) == 0 || meOut.Memberships[0].OrgID == "" {
		t.Fatalf("expected org membership from /auth/me, got %s", string(meBody))
	}
	orgID := meOut.Memberships[0].OrgID

	// Create mock upstream pool
	pRes, pBody := doOAJSON(t, http.MethodPost, ts.URL+"/api/v1/orgs/"+orgID+"/upstreams", map[string]any{
		"name":              "oa-pool",
		"destination_class": "mock_local",
		"targets":           []map[string]any{{"url": "http://mock-upstream:8089", "weight": 100}},
	}, cookies, loginOut.CSRFToken)
	if pRes.StatusCode != http.StatusCreated {
		t.Fatalf("create pool failed: %d %s", pRes.StatusCode, string(pBody))
	}
	var poolOut struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(pBody, &poolOut)

	// 1. Partial failure (route 1 valid, route 2 has traversal '..') MUST roll back entire transaction (including new service!)
	badImport := map[string]any{
		"service": map[string]any{
			"slug":        "rollback-svc",
			"name":        "Rollback Service",
			"description": "Should not persist on failure",
		},
		"upstream_pool_id": poolOut.ID,
		"routes": []map[string]any{
			{
				"key":         "k1",
				"action":      "create",
				"name":        "good-route",
				"host":        "api.acme.local",
				"path_prefix": "/v1/good",
				"methods":     []string{"GET"},
				"priority":    100,
			},
			{
				"key":         "k2",
				"action":      "create",
				"name":        "bad-route",
				"host":        "api.acme.local",
				"path_prefix": "/v1/../secret",
				"methods":     []string{"GET"},
				"priority":    100,
			},
		},
	}
	failRes, _ := doOAJSON(t, http.MethodPost, ts.URL+"/api/v1/orgs/"+orgID+"/openapi/import", badImport, cookies, loginOut.CSRFToken)
	if failRes.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 on partial failure, got %d", failRes.StatusCode)
	}

	var svcCount, routeCount int
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM services WHERE org_id = $1 AND slug = 'rollback-svc'`, orgID).Scan(&svcCount)
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM routes WHERE org_id = $1`, orgID).Scan(&routeCount)
	if svcCount != 0 || routeCount != 0 {
		t.Fatalf("expected full rollback (0 services, 0 routes), got services=%d routes=%d", svcCount, routeCount)
	}

	// 2. Valid preview + transactional import succeeds and records audit event
	specYAML := "openapi: '3.0.3'\ninfo:\n  title: Storefront API\n  version: '1.0.0'\nservers:\n  - url: https://store.acme.local/v1\npaths:\n  /products:\n    get:\n      operationId: listProducts\n"
	prevRes, prevBody := doOAJSON(t, http.MethodPost, ts.URL+"/api/v1/orgs/"+orgID+"/openapi/preview", map[string]any{
		"spec": specYAML,
	}, cookies, loginOut.CSRFToken)
	if prevRes.StatusCode != http.StatusOK {
		t.Fatalf("preview failed: %d %s", prevRes.StatusCode, string(prevBody))
	}
	var preview api.OpenAPIPreviewResponse
	_ = json.Unmarshal(prevBody, &preview)
	if len(preview.Routes) != 1 || preview.Routes[0].Conflict {
		t.Fatalf("unexpected preview: %+v", preview)
	}

	okImport := map[string]any{
		"service":          preview.Service,
		"upstream_pool_id": poolOut.ID,
		"routes": []map[string]any{
			{
				"key":         preview.Routes[0].Key,
				"action":      "create",
				"name":        preview.Routes[0].Name,
				"host":        preview.Routes[0].Host,
				"path_prefix": preview.Routes[0].PathPrefix,
				"methods":     preview.Routes[0].Methods,
				"priority":    preview.Routes[0].Priority,
			},
		},
	}
	impRes, impBody := doOAJSON(t, http.MethodPost, ts.URL+"/api/v1/orgs/"+orgID+"/openapi/import", okImport, cookies, loginOut.CSRFToken)
	if impRes.StatusCode != http.StatusCreated {
		t.Fatalf("import failed: %d %s", impRes.StatusCode, string(impBody))
	}
	if !bytes.Contains(impBody, []byte(`"created_routes":1`)) {
		t.Fatalf("expected created_routes:1, got %s", string(impBody))
	}

	// Verify audit event recorded
	var auditCount int
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_events WHERE org_id = $1 AND action = 'openapi.import'`, orgID).Scan(&auditCount)
	if auditCount != 1 {
		t.Fatalf("expected 1 openapi.import audit event, got %d", auditCount)
	}

	// 3. Verify OpenAPI export returns deterministic 3.0.3 JSON including imported route & limitations
	expRes, expBody := doOAJSON(t, http.MethodGet, ts.URL+"/api/v1/orgs/"+orgID+"/openapi/export", nil, cookies, "")
	if expRes.StatusCode != http.StatusOK {
		t.Fatalf("export failed: %d %s", expRes.StatusCode, string(expBody))
	}
	if !bytes.Contains(expBody, []byte(`"/v1/products"`)) || !bytes.Contains(expBody, []byte(`"x-gatewayforge-export-limitations"`)) {
		t.Fatalf("unexpected export body: %s", string(expBody))
	}
}
