package integration

import (
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

func TestPublishAndRollbackAuthorizationBoundaries(t *testing.T) {
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
	memH := &api.MembershipHandler{Store: st, Sessions: sessMgr, Hasher: hasher, Audit: auditRec}
	svcH := &api.ServiceHandler{Store: st, Sessions: sessMgr, Audit: auditRec}
	pubH := &api.PublicationHandler{Store: st, Sessions: sessMgr, Audit: auditRec, TargetPolicy: targetPol}

	router := api.NewRouter(api.RouterOptions{
		MountV1: func(v1 chi.Router) {
			authH.MountAuthRoutes(v1)
			memH.MountMembershipRoutes(v1)
			svcH.MountServiceRoutes(v1)
			pubH.MountPublicationRoutes(v1)
		},
	})
	ts := httptest.NewServer(router)
	defer ts.Close()

	// Bootstrap owner
	_, _ = doOAJSON(t, http.MethodPost, ts.URL+"/api/v1/auth/bootstrap", map[string]any{
		"email": "owner@rbac.test", "password": "SuperSecurePass!2026",
		"display_name": "Owner", "org_name": "RBAC Org", "org_slug": "rbac-org",
	}, nil, "")

	loginAs := func(email, pw string) ([]*http.Cookie, string) {
		r, b := doOAJSON(t, http.MethodPost, ts.URL+"/api/v1/auth/login", map[string]any{
			"email": email, "password": pw,
		}, nil, "")
		if r.StatusCode != http.StatusOK {
			t.Fatalf("login %s failed: %d %s", email, r.StatusCode, string(b))
		}
		var out struct {
			CSRFToken string `json:"csrf_token"`
		}
		_ = json.Unmarshal(b, &out)
		return r.Cookies(), out.CSRFToken
	}

	ownerCookies, ownerCSRF := loginAs("owner@rbac.test", "SuperSecurePass!2026")
	_, meBody := doOAJSON(t, http.MethodGet, ts.URL+"/api/v1/auth/me", nil, ownerCookies, "")
	var meOut struct {
		Memberships []struct {
			OrgID string `json:"org_id"`
		} `json:"memberships"`
	}
	_ = json.Unmarshal(meBody, &meOut)
	orgID := meOut.Memberships[0].OrgID

	// Provision developer, viewer, and admin members
	for _, m := range []struct{ email, name, role string }{
		{"dev@rbac.test", "Dev User", "developer"},
		{"viewer@rbac.test", "Viewer User", "viewer"},
		{"admin@rbac.test", "Admin User", "admin"},
	} {
		r, b := doOAJSON(t, http.MethodPost, ts.URL+"/api/v1/orgs/"+orgID+"/members", map[string]any{
			"email": m.email, "display_name": m.name, "password": "MemberSecurePass!2026", "role": m.role,
		}, ownerCookies, ownerCSRF)
		if r.StatusCode != http.StatusCreated {
			t.Fatalf("add member %s failed: %d %s", m.role, r.StatusCode, string(b))
		}
	}

	// Owner publishes v1 and v2
	r1, _ := doOAJSON(t, http.MethodPost, ts.URL+"/api/v1/orgs/"+orgID+"/publications", map[string]any{"notes": "v1"}, ownerCookies, ownerCSRF)
	r2, _ := doOAJSON(t, http.MethodPost, ts.URL+"/api/v1/orgs/"+orgID+"/publications", map[string]any{"notes": "v2"}, ownerCookies, ownerCSRF)
	if r1.StatusCode != http.StatusCreated || r2.StatusCode != http.StatusCreated {
		t.Fatalf("owner publish failed: v1=%d v2=%d", r1.StatusCode, r2.StatusCode)
	}

	// Developer: CAN edit draft, CANNOT publish or rollback/activate
	devCookies, devCSRF := loginAs("dev@rbac.test", "MemberSecurePass!2026")
	devSvc, _ := doOAJSON(t, http.MethodPost, ts.URL+"/api/v1/orgs/"+orgID+"/services", map[string]any{
		"slug": "dev-draft-svc", "name": "Dev Draft", "description": "",
	}, devCookies, devCSRF)
	if devSvc.StatusCode != http.StatusCreated {
		t.Fatalf("developer should be allowed to edit draft service, got %d", devSvc.StatusCode)
	}
	devDiff, _ := doOAJSON(t, http.MethodGet, ts.URL+"/api/v1/orgs/"+orgID+"/draft/diff", nil, devCookies, "")
	if devDiff.StatusCode != http.StatusOK {
		t.Fatalf("developer should read draft diff, got %d", devDiff.StatusCode)
	}
	devPub, _ := doOAJSON(t, http.MethodPost, ts.URL+"/api/v1/orgs/"+orgID+"/publications", map[string]any{"notes": "escalate"}, devCookies, devCSRF)
	if devPub.StatusCode != http.StatusForbidden {
		t.Fatalf("developer publish MUST be 403 Forbidden, got %d", devPub.StatusCode)
	}
	devRB, _ := doOAJSON(t, http.MethodPost, ts.URL+"/api/v1/orgs/"+orgID+"/publications/1/activate", map[string]any{"reason": "escalate"}, devCookies, devCSRF)
	if devRB.StatusCode != http.StatusForbidden {
		t.Fatalf("developer rollback/activate MUST be 403 Forbidden, got %d", devRB.StatusCode)
	}

	// Viewer: CANNOT edit draft, CANNOT publish or rollback
	viewerCookies, viewerCSRF := loginAs("viewer@rbac.test", "MemberSecurePass!2026")
	vSvc, _ := doOAJSON(t, http.MethodPost, ts.URL+"/api/v1/orgs/"+orgID+"/services", map[string]any{
		"slug": "viewer-svc", "name": "Viewer", "description": "",
	}, viewerCookies, viewerCSRF)
	if vSvc.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer draft write MUST be 403, got %d", vSvc.StatusCode)
	}
	vPub, _ := doOAJSON(t, http.MethodPost, ts.URL+"/api/v1/orgs/"+orgID+"/publications", map[string]any{"notes": "escalate"}, viewerCookies, viewerCSRF)
	if vPub.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer publish MUST be 403, got %d", vPub.StatusCode)
	}

	// Admin: CAN publish and rollback
	adminCookies, adminCSRF := loginAs("admin@rbac.test", "MemberSecurePass!2026")
	aPub, _ := doOAJSON(t, http.MethodPost, ts.URL+"/api/v1/orgs/"+orgID+"/publications", map[string]any{"notes": "admin v3"}, adminCookies, adminCSRF)
	if aPub.StatusCode != http.StatusCreated {
		t.Fatalf("admin publish should succeed 201, got %d", aPub.StatusCode)
	}
	aRB, _ := doOAJSON(t, http.MethodPost, ts.URL+"/api/v1/orgs/"+orgID+"/publications/1/activate", map[string]any{"reason": "admin rollback"}, adminCookies, adminCSRF)
	if aRB.StatusCode != http.StatusOK {
		t.Fatalf("admin rollback should succeed 200, got %d", aRB.StatusCode)
	}
}
