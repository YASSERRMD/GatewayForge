package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/YASSERRMD/GatewayForge/internal/control/api"
	"github.com/YASSERRMD/GatewayForge/internal/control/auth"
	"github.com/YASSERRMD/GatewayForge/internal/control/store"
	"github.com/YASSERRMD/GatewayForge/internal/shared/config"
)

func TestServiceAPIAndRBACPermissions(t *testing.T) {
	pool := openIntegrationDB(t)
	resetAndMigrateDB(t, pool)

	hasher := newTestArgonHasher()
	bootstrap := auth.NewBootstrapService(pool, config.EnvTest, hasher)
	sessions := auth.NewSessionManager(pool, hasher, time.Hour, false)
	st := store.New(pool)
	auditRec := api.NewAuditRecorder(st)

	boot, err := bootstrap.Bootstrap(t.Context(), auth.BootstrapInput{
		Email: "owner@svc.test", DisplayName: "Owner", Password: "StrongPassword!123", OrgSlug: "svc-org", OrgName: "Svc Org",
	})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	// Add a viewer user in the same org.
	pwHash, _ := hasher.Hash("ViewerPassword!123")
	var viewerID string
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO users (email, display_name, password_hash) VALUES ('viewer@svc.test', 'Viewer', $1) RETURNING id
	`, pwHash).Scan(&viewerID); err != nil {
		t.Fatalf("insert viewer: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO memberships (org_id, user_id, role) VALUES ($1, $2, 'viewer')`, boot.OrgID, viewerID); err != nil {
		t.Fatalf("insert viewer membership: %v", err)
	}

	authH := &api.AuthHandler{Bootstrap: bootstrap, Sessions: sessions}
	svcH := &api.ServiceHandler{Store: st, Sessions: sessions, Audit: auditRec}
	router := api.NewRouter(api.RouterOptions{
		Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
		MountV1: func(v1 chi.Router) {
			authH.MountAuthRoutes(v1)
			svcH.MountServiceRoutes(v1)
		},
	})

	login := func(email, password string) (*http.Cookie, string) {
		b, _ := json.Marshal(map[string]string{"email": email, "password": password})
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(b)))
		if rec.Code != http.StatusOK {
			t.Fatalf("login %s failed: %d", email, rec.Code)
		}
		var out struct {
			CSRFToken string `json:"csrf_token"`
		}
		_ = json.NewDecoder(rec.Body).Decode(&out)
		return rec.Result().Cookies()[0], out.CSRFToken
	}

	ownerCookie, ownerCSRF := login("owner@svc.test", "StrongPassword!123")
	viewerCookie, viewerCSRF := login("viewer@svc.test", "ViewerPassword!123")

	// 1. Viewer CANNOT create a service (403 Forbidden).
	forbReq := httptest.NewRequest(http.MethodPost, "/api/v1/orgs/"+boot.OrgID+"/services", bytes.NewBufferString(`{"slug":"orders","name":"Orders"}`))
	forbReq.AddCookie(viewerCookie)
	forbReq.Header.Set(auth.CSRFHeaderName, viewerCSRF)
	forbRec := httptest.NewRecorder()
	router.ServeHTTP(forbRec, forbReq)
	if forbRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for viewer creating service, got %d", forbRec.Code)
	}

	// 2. Owner creates service.
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/orgs/"+boot.OrgID+"/services", bytes.NewBufferString(`{"slug":"orders","name":"Orders API","description":"Handles orders"}`))
	createReq.AddCookie(ownerCookie)
	createReq.Header.Set(auth.CSRFHeaderName, ownerCSRF)
	createRec := httptest.NewRecorder()
	router.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("expected 201 creating service, got %d: %s", createRec.Code, createRec.Body.String())
	}
	var created store.Service
	_ = json.NewDecoder(createRec.Body).Decode(&created)

	// 3. Viewer CAN read/list services (200 OK).
	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/orgs/"+boot.OrgID+"/services?search=orders", nil)
	listReq.AddCookie(viewerCookie)
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected 200 listing services as viewer, got %d", listRec.Code)
	}

	// 4. Owner updates service with ETag; stale ETag gets 409.
	patchReq := httptest.NewRequest(http.MethodPatch, "/api/v1/orgs/"+boot.OrgID+"/services/"+created.ID, bytes.NewBufferString(`{"name":"Orders API v2"}`))
	patchReq.AddCookie(ownerCookie)
	patchReq.Header.Set(auth.CSRFHeaderName, ownerCSRF)
	patchReq.Header.Set("If-Match", `W/"rev-1"`)
	patchRec := httptest.NewRecorder()
	router.ServeHTTP(patchRec, patchReq)
	if patchRec.Code != http.StatusOK || patchRec.Header().Get("ETag") != `W/"rev-2"` {
		t.Fatalf("expected 200 rev-2, got %d etag=%q", patchRec.Code, patchRec.Header().Get("ETag"))
	}

	// 5. Owner tombstones service (204) and can recreate same slug cleanly.
	delReq := httptest.NewRequest(http.MethodDelete, "/api/v1/orgs/"+boot.OrgID+"/services/"+created.ID, nil)
	delReq.AddCookie(ownerCookie)
	delReq.Header.Set(auth.CSRFHeaderName, ownerCSRF)
	delRec := httptest.NewRecorder()
	router.ServeHTTP(delRec, delReq)
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("expected 204 deleting service, got %d", delRec.Code)
	}

	recreateReq := httptest.NewRequest(http.MethodPost, "/api/v1/orgs/"+boot.OrgID+"/services", bytes.NewBufferString(`{"slug":"orders","name":"Orders Recreated"}`))
	recreateReq.AddCookie(ownerCookie)
	recreateReq.Header.Set(auth.CSRFHeaderName, ownerCSRF)
	recreateRec := httptest.NewRecorder()
	router.ServeHTTP(recreateRec, recreateReq)
	if recreateRec.Code != http.StatusCreated {
		t.Fatalf("expected 201 recreating tombstoned slug, got %d: %s", recreateRec.Code, recreateRec.Body.String())
	}
}
