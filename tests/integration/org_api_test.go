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

func TestOrganizationCRUDAndTenantIsolation(t *testing.T) {
	pool := openIntegrationDB(t)
	resetAndMigrateDB(t, pool)

	hasher := newTestArgonHasher()
	bootstrap := auth.NewBootstrapService(pool, config.EnvTest, hasher)
	sessions := auth.NewSessionManager(pool, hasher, time.Hour, false)
	st := store.New(pool)

	boot, err := bootstrap.Bootstrap(t.Context(), auth.BootstrapInput{
		Email: "owner@orgs.test", DisplayName: "Owner", Password: "StrongPassword!123", OrgSlug: "primary-org", OrgName: "Primary Org",
	})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	// Create a second user with NO membership in primary-org.
	pw2, err := hasher.Hash("OtherStrongPassword!123")
	if err != nil {
		t.Fatalf("hash pw2: %v", err)
	}
	var user2ID string
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO users (email, display_name, password_hash) VALUES ('other@orgs.test', 'Other', $1) RETURNING id
	`, pw2).Scan(&user2ID); err != nil {
		t.Fatalf("insert user2: %v", err)
	}

	authH := &api.AuthHandler{Bootstrap: bootstrap, Sessions: sessions}
	orgH := &api.OrgHandler{Store: st, Sessions: sessions}
	router := api.NewRouter(api.RouterOptions{
		Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
		MountV1: func(v1 chi.Router) {
			authH.MountAuthRoutes(v1)
			orgH.MountOrgRoutes(v1)
		},
	})

	// Helper to login and obtain cookie + csrf token.
	loginAs := func(email, password string) (*http.Cookie, string) {
		body, _ := json.Marshal(map[string]string{"email": email, "password": password})
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body)))
		if rec.Code != http.StatusOK {
			t.Fatalf("login %s failed: %d %s", email, rec.Code, rec.Body.String())
		}
		var out struct {
			CSRFToken string `json:"csrf_token"`
		}
		_ = json.NewDecoder(rec.Body).Decode(&out)
		return rec.Result().Cookies()[0], out.CSRFToken
	}

	ownerCookie, ownerCSRF := loginAs("owner@orgs.test", "StrongPassword!123")
	otherCookie, otherCSRF := loginAs("other@orgs.test", "OtherStrongPassword!123")

	// 1. Owner creates a second org.
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/orgs", bytes.NewBufferString(`{"slug":"second-org","display_name":"Second Org"}`))
	createReq.AddCookie(ownerCookie)
	createReq.Header.Set(auth.CSRFHeaderName, ownerCSRF)
	createRec := httptest.NewRecorder()
	router.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("expected 201 creating org, got %d: %s", createRec.Code, createRec.Body.String())
	}
	var secondOrg store.Organization
	if err := json.NewDecoder(createRec.Body).Decode(&secondOrg); err != nil {
		t.Fatalf("decode created org: %v", err)
	}

	// 2. Paginated list for owner returns 2 orgs; list for user2 returns 0.
	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/orgs?limit=1&offset=0", nil)
	listReq.AddCookie(ownerCookie)
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	var page struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}
	_ = json.NewDecoder(listRec.Body).Decode(&page)
	if listRec.Code != http.StatusOK || page.Total != 2 || len(page.Items) != 1 {
		t.Fatalf("unexpected owner org page: code=%d total=%d len=%d", listRec.Code, page.Total, len(page.Items))
	}

	otherListReq := httptest.NewRequest(http.MethodGet, "/api/v1/orgs", nil)
	otherListReq.AddCookie(otherCookie)
	otherListRec := httptest.NewRecorder()
	router.ServeHTTP(otherListRec, otherListReq)
	var otherPage struct {
		Total int `json:"total"`
	}
	_ = json.NewDecoder(otherListRec.Body).Decode(&otherPage)
	if otherPage.Total != 0 {
		t.Fatalf("expected user2 to see 0 orgs, got %d", otherPage.Total)
	}

	// 3. User2 CANNOT read or patch owner's primary org.
	forbGet := httptest.NewRequest(http.MethodGet, "/api/v1/orgs/"+boot.OrgID, nil)
	forbGet.AddCookie(otherCookie)
	forbRec := httptest.NewRecorder()
	router.ServeHTTP(forbRec, forbGet)
	if forbRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for cross-tenant GET org, got %d", forbRec.Code)
	}

	forbPatch := httptest.NewRequest(http.MethodPatch, "/api/v1/orgs/"+boot.OrgID, bytes.NewBufferString(`{"display_name":"Hacked","revision":1}`))
	forbPatch.AddCookie(otherCookie)
	forbPatch.Header.Set(auth.CSRFHeaderName, otherCSRF)
	forbPatchRec := httptest.NewRecorder()
	router.ServeHTTP(forbPatchRec, forbPatch)
	if forbPatchRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for cross-tenant PATCH org, got %d", forbPatchRec.Code)
	}

	// 4. Owner updates secondOrg with If-Match ETag; stale ETag gets 409 Conflict.
	patchReq := httptest.NewRequest(http.MethodPatch, "/api/v1/orgs/"+secondOrg.ID, bytes.NewBufferString(`{"display_name":"Second Org Updated"}`))
	patchReq.AddCookie(ownerCookie)
	patchReq.Header.Set(auth.CSRFHeaderName, ownerCSRF)
	patchReq.Header.Set("If-Match", `W/"rev-1"`)
	patchRec := httptest.NewRecorder()
	router.ServeHTTP(patchRec, patchReq)
	if patchRec.Code != http.StatusOK || patchRec.Header().Get("ETag") != `W/"rev-2"` {
		t.Fatalf("expected 200 and ETag rev-2, got %d ETag=%q body=%s", patchRec.Code, patchRec.Header().Get("ETag"), patchRec.Body.String())
	}

	staleReq := httptest.NewRequest(http.MethodPatch, "/api/v1/orgs/"+secondOrg.ID, bytes.NewBufferString(`{"display_name":"Stale"}`))
	staleReq.AddCookie(ownerCookie)
	staleReq.Header.Set(auth.CSRFHeaderName, ownerCSRF)
	staleReq.Header.Set("If-Match", `W/"rev-1"`)
	staleRec := httptest.NewRecorder()
	router.ServeHTTP(staleRec, staleReq)
	if staleRec.Code != http.StatusConflict {
		t.Fatalf("expected 409 on stale If-Match, got %d", staleRec.Code)
	}
}
