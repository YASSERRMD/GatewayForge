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
	"github.com/YASSERRMD/GatewayForge/internal/shared/netsec"
	"github.com/YASSERRMD/GatewayForge/internal/shared/snapshot"
)

func TestAuthenticatedSnapshotRetrievalAndRotation(t *testing.T) {
	pool := openIntegrationDB(t)
	resetAndMigrateDB(t, pool)

	hasher := newTestArgonHasher()
	bootstrap := auth.NewBootstrapService(pool, config.EnvTest, hasher)
	sessions := auth.NewSessionManager(pool, hasher, time.Hour, false)
	st := store.New(pool)

	boot, err := bootstrap.Bootstrap(t.Context(), auth.BootstrapInput{
		Email: "owner@sync.test", DisplayName: "Owner", Password: "StrongPassword!123", OrgSlug: "sync-org", OrgName: "Sync Org",
	})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	svc, _ := st.CreateService(t.Context(), boot.OrgID, boot.EnvID, "echo", "Echo", "")
	upPool, _ := st.CreateUpstreamPool(t.Context(), store.UpstreamPool{
		OrgID: boot.OrgID, EnvID: boot.EnvID, Name: "echo-pool", DestinationClass: "mock_local",
	})
	_, _ = st.AddUpstreamTarget(t.Context(), boot.OrgID, upPool.ID, "http://mock-upstream:8089", 100, true)
	_, _ = st.CreateRoute(t.Context(), store.Route{
		OrgID: boot.OrgID, EnvID: boot.EnvID, ServiceID: svc.ID, UpstreamPoolID: upPool.ID,
		Name: "echo-route", Host: "api.local", PathPrefix: "/echo", Methods: []string{"GET"}, Priority: 100, Enabled: true,
	})

	authH := &api.AuthHandler{Bootstrap: bootstrap, Sessions: sessions}
	pubH := &api.PublicationHandler{Store: st, Sessions: sessions, TargetPolicy: netsec.DefaultTargetPolicy(true)}
	syncH := &api.SyncHandler{Store: st, SyncTokens: api.ParseSyncTokens("new-token-1234567890,old-token-0987654321")}

	router := api.NewRouter(api.RouterOptions{
		Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
		MountV1: func(v1 chi.Router) {
			authH.MountAuthRoutes(v1)
			pubH.MountPublicationRoutes(v1)
			syncH.MountGatewaySyncRoutes(v1)
		},
	})

	// Publish v1 as owner.
	loginRec := httptest.NewRecorder()
	router.ServeHTTP(loginRec, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{"email":"owner@sync.test","password":"StrongPassword!123"}`)))
	cookie := loginRec.Result().Cookies()[0]
	var loginOut struct {
		CSRFToken string `json:"csrf_token"`
	}
	_ = json.NewDecoder(loginRec.Body).Decode(&loginOut)

	pubReq := httptest.NewRequest(http.MethodPost, "/api/v1/orgs/"+boot.OrgID+"/publications", bytes.NewBufferString(`{"notes":"v1"}`))
	pubReq.AddCookie(cookie)
	pubReq.Header.Set(auth.CSRFHeaderName, loginOut.CSRFToken)
	pubRec := httptest.NewRecorder()
	router.ServeHTTP(pubRec, pubReq)
	if pubRec.Code != http.StatusCreated {
		t.Fatalf("publish v1 failed: %d %s", pubRec.Code, pubRec.Body.String())
	}

	// 1. Unauthorized (no token / bad token) MUST return 401.
	noTok := httptest.NewRecorder()
	router.ServeHTTP(noTok, httptest.NewRequest(http.MethodGet, "/api/v1/gateway/snapshots/active", nil))
	if noTok.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without sync token, got %d", noTok.Code)
	}
	badTokReq := httptest.NewRequest(http.MethodGet, "/api/v1/gateway/snapshots/active", nil)
	badTokReq.Header.Set("Authorization", "Bearer wrong-token")
	badTok := httptest.NewRecorder()
	router.ServeHTTP(badTok, badTokReq)
	if badTok.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with bad sync token, got %d", badTok.Code)
	}

	// 2. Both new and old rotation tokens succeed with verified checksum and ETag 304 support.
	for _, tok := range []string{"new-token-1234567890", "old-token-0987654321"} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/gateway/snapshots/active", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("token %q expected 200, got %d: %s", tok, rec.Code, rec.Body.String())
		}
		var snap snapshot.Snapshot
		if err := json.NewDecoder(rec.Body).Decode(&snap); err != nil {
			t.Fatalf("decode snapshot: %v", err)
		}
		if err := snap.VerifyChecksum(); err != nil {
			t.Fatalf("snapshot checksum invalid: %v", err)
		}
		if len(snap.Routes) != 1 || snap.Routes[0].Name != "echo-route" {
			t.Fatalf("unexpected routes in active snapshot: %+v", snap.Routes)
		}

		// Conditional GET with matching ETag returns 304 Not Modified.
		req304 := httptest.NewRequest(http.MethodGet, "/api/v1/gateway/snapshots/active", nil)
		req304.Header.Set("Authorization", "Bearer "+tok)
		req304.Header.Set("If-None-Match", rec.Header().Get("ETag"))
		rec304 := httptest.NewRecorder()
		router.ServeHTTP(rec304, req304)
		if rec304.Code != http.StatusNotModified {
			t.Fatalf("expected 304 Not Modified, got %d", rec304.Code)
		}
	}
}
