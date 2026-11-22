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

func TestCurrentUserAndMultiOrgContextIsolation(t *testing.T) {
	pool := openIntegrationDB(t)
	resetAndMigrateDB(t, pool)

	hasher := newTestArgonHasher()
	bootstrap := auth.NewBootstrapService(pool, config.EnvTest, hasher)
	sessions := auth.NewSessionManager(pool, hasher, time.Hour, false)
	st := store.New(pool)

	res, err := bootstrap.Bootstrap(t.Context(), auth.BootstrapInput{
		Email:       "multi@acme.test",
		DisplayName: "Multi User",
		Password:    "StrongPassword!999",
		OrgSlug:     "org-one",
		OrgName:     "Org One",
	})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	// Create orgTwo (user IS member as viewer) and orgThree (user is NOT a member).
	orgTwo, err := st.CreateOrganization(t.Context(), "org-two", "Org Two")
	if err != nil {
		t.Fatalf("create org-two: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO memberships (org_id, user_id, role) VALUES ($1, $2, 'viewer')`, orgTwo.ID, res.UserID); err != nil {
		t.Fatalf("add org-two membership: %v", err)
	}
	orgThree, err := st.CreateOrganization(t.Context(), "org-three", "Org Three")
	if err != nil {
		t.Fatalf("create org-three: %v", err)
	}

	authHandler := &api.AuthHandler{Bootstrap: bootstrap, Sessions: sessions}
	router := api.NewRouter(api.RouterOptions{
		Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
		MountV1: func(v1 chi.Router) {
			authHandler.MountAuthRoutes(v1)
			v1.With(api.RequireAuth(sessions), api.RequireOrgContext(sessions)).Get("/orgs/{orgId}/context-check", func(w http.ResponseWriter, r *http.Request) {
				mem, _ := api.OrgContextFromContext(r.Context())
				api.WriteJSON(w, http.StatusOK, mem)
			})
		},
	})

	// Login
	loginRec := httptest.NewRecorder()
	router.ServeHTTP(loginRec, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{"email":"multi@acme.test","password":"StrongPassword!999"}`)))
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login failed: %d", loginRec.Code)
	}
	cookie := loginRec.Result().Cookies()[0]

	// 1. /auth/me lists exactly org-one (owner) and org-two (viewer), NOT org-three.
	meReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	meReq.AddCookie(cookie)
	meRec := httptest.NewRecorder()
	router.ServeHTTP(meRec, meReq)
	if meRec.Code != http.StatusOK {
		t.Fatalf("GET /auth/me expected 200, got %d: %s", meRec.Code, meRec.Body.String())
	}
	var mePayload struct {
		Memberships []auth.OrgMembership `json:"memberships"`
	}
	if err := json.NewDecoder(meRec.Body).Decode(&mePayload); err != nil {
		t.Fatalf("decode /auth/me: %v", err)
	}
	if len(mePayload.Memberships) != 2 || mePayload.Memberships[0].OrgSlug != "org-one" || mePayload.Memberships[1].OrgSlug != "org-two" {
		t.Fatalf("unexpected memberships: %+v", mePayload.Memberships)
	}

	// 2. Accessing org-two succeeds with viewer role.
	okReq := httptest.NewRequest(http.MethodGet, "/api/v1/orgs/"+orgTwo.ID+"/context-check", nil)
	okReq.AddCookie(cookie)
	okRec := httptest.NewRecorder()
	router.ServeHTTP(okRec, okReq)
	if okRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for org-two, got %d", okRec.Code)
	}

	// 3. Accessing org-three (non-member) MUST return 403 Forbidden (no implicit fallback).
	forbReq := httptest.NewRequest(http.MethodGet, "/api/v1/orgs/"+orgThree.ID+"/context-check", nil)
	forbReq.AddCookie(cookie)
	forbRec := httptest.NewRecorder()
	router.ServeHTTP(forbRec, forbReq)
	if forbRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-member org-three, got %d", forbRec.Code)
	}
}
