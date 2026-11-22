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

func TestMembershipEscalationAndLastOwnerInvariant(t *testing.T) {
	pool := openIntegrationDB(t)
	resetAndMigrateDB(t, pool)

	hasher := newTestArgonHasher()
	bootstrap := auth.NewBootstrapService(pool, config.EnvTest, hasher)
	sessions := auth.NewSessionManager(pool, hasher, time.Hour, false)
	st := store.New(pool)

	boot, err := bootstrap.Bootstrap(t.Context(), auth.BootstrapInput{
		Email: "owner@mem.test", DisplayName: "Owner", Password: "StrongPassword!123", OrgSlug: "mem-org", OrgName: "Mem Org",
	})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	authH := &api.AuthHandler{Bootstrap: bootstrap, Sessions: sessions}
	memH := &api.MembershipHandler{Store: st, Sessions: sessions, Hasher: hasher}
	router := api.NewRouter(api.RouterOptions{
		Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
		MountV1: func(v1 chi.Router) {
			authH.MountAuthRoutes(v1)
			memH.MountMembershipRoutes(v1)
		},
	})

	loginAs := func(email, password string) (*http.Cookie, string) {
		body, _ := json.Marshal(map[string]string{"email": email, "password": password})
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body)))
		if rec.Code != http.StatusOK {
			t.Fatalf("login %s failed: %d", email, rec.Code)
		}
		var out struct {
			CSRFToken string `json:"csrf_token"`
		}
		_ = json.NewDecoder(rec.Body).Decode(&out)
		return rec.Result().Cookies()[0], out.CSRFToken
	}

	ownerCookie, ownerCSRF := loginAs("owner@mem.test", "StrongPassword!123")

	// 1. Owner cannot demote or delete themselves as the LAST owner (409 Conflict).
	demoteSelf := httptest.NewRequest(http.MethodPatch, "/api/v1/orgs/"+boot.OrgID+"/members/"+boot.UserID, bytes.NewBufferString(`{"role":"admin"}`))
	demoteSelf.AddCookie(ownerCookie)
	demoteSelf.Header.Set(auth.CSRFHeaderName, ownerCSRF)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, demoteSelf)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 demoting last owner, got %d: %s", rec.Code, rec.Body.String())
	}

	delSelf := httptest.NewRequest(http.MethodDelete, "/api/v1/orgs/"+boot.OrgID+"/members/"+boot.UserID, nil)
	delSelf.AddCookie(ownerCookie)
	delSelf.Header.Set(auth.CSRFHeaderName, ownerCSRF)
	recDel := httptest.NewRecorder()
	router.ServeHTTP(recDel, delSelf)
	if recDel.Code != http.StatusConflict {
		t.Fatalf("expected 409 deleting last owner, got %d: %s", recDel.Code, recDel.Body.String())
	}

	// 2. Owner adds an admin user.
	addAdmin := httptest.NewRequest(http.MethodPost, "/api/v1/orgs/"+boot.OrgID+"/members",
		bytes.NewBufferString(`{"email":"admin@mem.test","display_name":"Org Admin","password":"AdminPassword!123","role":"admin"}`))
	addAdmin.AddCookie(ownerCookie)
	addAdmin.Header.Set(auth.CSRFHeaderName, ownerCSRF)
	addRec := httptest.NewRecorder()
	router.ServeHTTP(addRec, addAdmin)
	if addRec.Code != http.StatusCreated {
		t.Fatalf("expected 201 adding admin, got %d: %s", addRec.Code, addRec.Body.String())
	}
	var adminAdded struct {
		UserID string `json:"user_id"`
	}
	_ = json.NewDecoder(addRec.Body).Decode(&adminAdded)

	// 3. Admin CANNOT escalate themselves to owner or demote the existing owner (403 Forbidden).
	adminCookie, adminCSRF := loginAs("admin@mem.test", "AdminPassword!123")
	escalate := httptest.NewRequest(http.MethodPatch, "/api/v1/orgs/"+boot.OrgID+"/members/"+adminAdded.UserID, bytes.NewBufferString(`{"role":"owner"}`))
	escalate.AddCookie(adminCookie)
	escalate.Header.Set(auth.CSRFHeaderName, adminCSRF)
	escRec := httptest.NewRecorder()
	router.ServeHTTP(escRec, escalate)
	if escRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when admin attempts owner self-escalation, got %d", escRec.Code)
	}

	// 4. Owner promotes admin to second owner, then demoting the first owner succeeds!
	promote := httptest.NewRequest(http.MethodPatch, "/api/v1/orgs/"+boot.OrgID+"/members/"+adminAdded.UserID, bytes.NewBufferString(`{"role":"owner"}`))
	promote.AddCookie(ownerCookie)
	promote.Header.Set(auth.CSRFHeaderName, ownerCSRF)
	promRec := httptest.NewRecorder()
	router.ServeHTTP(promRec, promote)
	if promRec.Code != http.StatusOK {
		t.Fatalf("expected 200 promoting second owner, got %d: %s", promRec.Code, promRec.Body.String())
	}

	demoteNow := httptest.NewRequest(http.MethodPatch, "/api/v1/orgs/"+boot.OrgID+"/members/"+boot.UserID, bytes.NewBufferString(`{"role":"admin"}`))
	demoteNow.AddCookie(ownerCookie)
	demoteNow.Header.Set(auth.CSRFHeaderName, ownerCSRF)
	demoteNowRec := httptest.NewRecorder()
	router.ServeHTTP(demoteNowRec, demoteNow)
	if demoteNowRec.Code != http.StatusOK {
		t.Fatalf("expected 200 demoting first owner when second owner exists, got %d: %s", demoteNowRec.Code, demoteNowRec.Body.String())
	}
}
