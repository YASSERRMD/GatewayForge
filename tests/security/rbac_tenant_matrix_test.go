package security

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/YASSERRMD/GatewayForge/internal/control/api"
	"github.com/YASSERRMD/GatewayForge/internal/control/auth"
	"github.com/YASSERRMD/GatewayForge/internal/control/migrate"
	"github.com/YASSERRMD/GatewayForge/internal/control/rbac"
	"github.com/YASSERRMD/GatewayForge/internal/control/store"
	"github.com/YASSERRMD/GatewayForge/internal/shared/netsec"
	"github.com/YASSERRMD/GatewayForge/migrations"
)

func TestRBACNegativePermissionMatrix(t *testing.T) {
	// Verify deny-by-default for unknown/empty roles
	if rbac.Allows(rbac.Role(""), rbac.PermOrgRead) || rbac.Allows(rbac.Role("superadmin"), rbac.PermConfigRead) {
		t.Fatal("unknown or empty role must be denied by default")
	}

	// Strict negative assertions per role
	forbidden := map[rbac.Role][]rbac.Permission{
		rbac.RoleViewer: {
			rbac.PermOrgUpdate, rbac.PermOrgDelete, rbac.PermMemberWrite, rbac.PermMemberOwnerGrant,
			rbac.PermConfigWrite, rbac.PermConsumerWrite, rbac.PermKeyManage,
			rbac.PermReleasePublish, rbac.PermReleaseActivate, rbac.PermReleaseRollback, rbac.PermConsoleUse,
		},
		rbac.RoleDeveloper: {
			rbac.PermOrgUpdate, rbac.PermOrgDelete, rbac.PermMemberWrite, rbac.PermMemberOwnerGrant,
			rbac.PermConsumerWrite, rbac.PermKeyManage,
			rbac.PermReleasePublish, rbac.PermReleaseActivate, rbac.PermReleaseRollback,
		},
		rbac.RoleAdmin: {
			rbac.PermOrgDelete, rbac.PermMemberOwnerGrant,
		},
	}

	for role, perms := range forbidden {
		for _, p := range perms {
			if rbac.Allows(role, p) {
				t.Fatalf("privilege escalation: role %q must NOT be granted %q", role, p)
			}
		}
	}
}

func openSecDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if os.Getenv("INTEGRATION_TESTS") != "1" {
		t.Skip("set INTEGRATION_TESTS=1 to run live PostgreSQL security matrix")
	}
	dsn := os.Getenv("INTEGRATION_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://gatewayforge:gatewayforge_local_dev_pw@127.0.0.1:5439/gatewayforge?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect db: %v", err)
	}
	if _, err := pool.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public;`); err != nil {
		t.Fatalf("reset schema: %v", err)
	}
	if _, err := migrate.Up(ctx, pool, migrations.Files); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	return pool
}

func doSecJSON(t *testing.T, method, url string, payload any, cookies []*http.Cookie, csrf string) (*http.Response, []byte) {
	t.Helper()
	var body io.Reader
	if payload != nil {
		b, _ := json.Marshal(payload)
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatalf("new req: %v", err)
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
		t.Fatalf("do req: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	return resp, raw
}

func TestCrossTenantIsolationAndIDORMatrix(t *testing.T) {
	pool := openSecDB(t)
	defer pool.Close()

	st := store.New(pool)
	hasher := auth.NewArgon2idHasher(auth.DefaultArgon2idParams())
	bootstrapSvc := auth.NewBootstrapService(pool, "test", hasher)
	sessMgr := auth.NewSessionManager(pool, hasher, time.Hour, false)
	auditRec := api.NewAuditRecorder(st)
	targetPol := netsec.DefaultTargetPolicy(true)

	authH := &api.AuthHandler{Bootstrap: bootstrapSvc, Sessions: sessMgr}
	orgH := &api.OrgHandler{Store: st, Sessions: sessMgr, Audit: auditRec}
	memH := &api.MembershipHandler{Store: st, Sessions: sessMgr, Hasher: hasher, Audit: auditRec}
	svcH := &api.ServiceHandler{Store: st, Sessions: sessMgr, Audit: auditRec}
	upH := &api.UpstreamHandler{Store: st, Sessions: sessMgr, Audit: auditRec, TargetPolicy: targetPol}
	rtH := &api.RouteHandler{Store: st, Sessions: sessMgr, Audit: auditRec}
	consH := &api.ConsumerHandler{Store: st, Sessions: sessMgr, Audit: auditRec, KeySecret: []byte("test-hmac-secret-key-32-bytes-long")}
	pubH := &api.PublicationHandler{Store: st, Sessions: sessMgr, Audit: auditRec, TargetPolicy: targetPol}

	router := api.NewRouter(api.RouterOptions{
		MountV1: func(v1 chi.Router) {
			authH.MountAuthRoutes(v1)
			orgH.MountOrgRoutes(v1)
			memH.MountMembershipRoutes(v1)
			svcH.MountServiceRoutes(v1)
			upH.MountUpstreamRoutes(v1)
			rtH.MountRouteRoutes(v1)
			consH.MountConsumerRoutes(v1)
			pubH.MountPublicationRoutes(v1)
		},
	})
	ts := httptest.NewServer(router)
	defer ts.Close()

	// Bootstrap Owner A in Org A
	_, _ = doSecJSON(t, http.MethodPost, ts.URL+"/api/v1/auth/bootstrap", map[string]any{
		"email": "ownera@tenant-a.test", "password": "SuperSecurePass!2026",
		"display_name": "Owner A", "org_name": "Tenant A", "org_slug": "tenant-a",
	}, nil, "")

	login := func(email, pw string) ([]*http.Cookie, string) {
		r, b := doSecJSON(t, http.MethodPost, ts.URL+"/api/v1/auth/login", map[string]any{"email": email, "password": pw}, nil, "")
		if r.StatusCode != http.StatusOK {
			t.Fatalf("login %s failed: %d %s", email, r.StatusCode, string(b))
		}
		var out struct {
			CSRFToken string `json:"csrf_token"`
		}
		_ = json.Unmarshal(b, &out)
		return r.Cookies(), out.CSRFToken
	}

	cookiesA, csrfA := login("ownera@tenant-a.test", "SuperSecurePass!2026")
	_, meBodyA := doSecJSON(t, http.MethodGet, ts.URL+"/api/v1/auth/me", nil, cookiesA, "")
	var meA struct {
		Memberships []struct {
			OrgID string `json:"org_id"`
		} `json:"memberships"`
	}
	_ = json.Unmarshal(meBodyA, &meA)
	orgA := meA.Memberships[0].OrgID

	// Create Org B via Owner A, add Owner B, then remove Owner A from Org B? Even cleaner: create Org B directly via store + User B!
	orgB, err := st.CreateOrganization(context.Background(), "tenant-b", "Tenant B")
	if err != nil {
		t.Fatalf("create org B: %v", err)
	}
	_, _ = st.CreateEnvironment(context.Background(), orgB.ID, "dev", "Development", true)
	pwHash, _ := hasher.Hash("SuperSecurePass!2026")
	var userBID string
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO users (email, display_name, password_hash) VALUES ($1, $2, $3) RETURNING id
	`, "ownerb@tenant-b.test", "Owner B", pwHash).Scan(&userBID); err != nil {
		t.Fatalf("create user B: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO memberships (org_id, user_id, role) VALUES ($1, $2, 'owner')
	`, orgB.ID, userBID); err != nil {
		t.Fatalf("membership B: %v", err)
	}

	cookiesB, csrfB := login("ownerb@tenant-b.test", "SuperSecurePass!2026")
	// Owner B creates resources in Org B
	_, svcBBody := doSecJSON(t, http.MethodPost, ts.URL+"/api/v1/orgs/"+orgB.ID+"/services", map[string]any{
		"slug": "secret-b", "name": "Secret B", "description": "",
	}, cookiesB, csrfB)
	var svcB struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(svcBBody, &svcB)

	_, poolBBody := doSecJSON(t, http.MethodPost, ts.URL+"/api/v1/orgs/"+orgB.ID+"/upstreams", map[string]any{
		"name": "pool-b", "destination_class": "mock_local",
		"targets": []map[string]any{{"url": "http://mock-upstream:8089", "weight": 100}},
	}, cookiesB, csrfB)
	var poolB struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(poolBBody, &poolB)

	// 1. Unauthenticated callers receive 401
	unauthRes, _ := doSecJSON(t, http.MethodGet, ts.URL+"/api/v1/orgs/"+orgB.ID+"/services", nil, nil, "")
	if unauthRes.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 unauthenticated, got %d", unauthRes.StatusCode)
	}

	// 2. Missing CSRF token on state-changing call receives 403
	noCSRF, _ := doSecJSON(t, http.MethodPost, ts.URL+"/api/v1/orgs/"+orgA+"/services", map[string]any{
		"slug": "nocsrf", "name": "No CSRF",
	}, cookiesA, "")
	if noCSRF.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 missing CSRF, got %d", noCSRF.StatusCode)
	}

	// 3. Cross-tenant access: Owner A cannot read/mutate/publish Org B resources (403)
	crossEndpoints := []struct {
		method string
		path   string
		body   any
	}{
		{http.MethodGet, "/services", nil},
		{http.MethodGet, "/services/" + svcB.ID, nil},
		{http.MethodDelete, "/services/" + svcB.ID, nil},
		{http.MethodGet, "/upstreams", nil},
		{http.MethodGet, "/routes", nil},
		{http.MethodGet, "/consumers", nil},
		{http.MethodGet, "/publications", nil},
		{http.MethodPost, "/publications", map[string]any{"notes": "cross-tenant hijack"}},
	}
	for _, ep := range crossEndpoints {
		r, _ := doSecJSON(t, ep.method, ts.URL+"/api/v1/orgs/"+orgB.ID+ep.path, ep.body, cookiesA, csrfA)
		if r.StatusCode != http.StatusForbidden {
			t.Fatalf("cross-tenant %s %s expected 403, got %d", ep.method, ep.path, r.StatusCode)
		}
	}

	// 4. Cross-tenant IDOR reference injection: Owner A cannot create a route in Org A pointing to Org B's service/pool!
	idorRes, _ := doSecJSON(t, http.MethodPost, ts.URL+"/api/v1/orgs/"+orgA+"/routes", map[string]any{
		"service_id":       svcB.ID,
		"upstream_pool_id": poolB.ID,
		"name":             "idor-route",
		"host":             "hijack.local",
		"path_prefix":      "/steal",
		"methods":          []string{"GET"},
		"priority":         100,
	}, cookiesA, csrfA)
	if idorRes.StatusCode != http.StatusUnprocessableEntity && idorRes.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-tenant IDOR route creation expected 422/403, got %d", idorRes.StatusCode)
	}
}
