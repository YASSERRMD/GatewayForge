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

func TestAPIKeyRevokeAndRotatePropagatesToActiveSnapshot(t *testing.T) {
	pool := openIntegrationDB(t)
	resetAndMigrateDB(t, pool)

	hasher := newTestArgonHasher()
	bootstrap := auth.NewBootstrapService(pool, config.EnvTest, hasher)
	sessions := auth.NewSessionManager(pool, hasher, time.Hour, false)
	st := store.New(pool)

	boot, err := bootstrap.Bootstrap(t.Context(), auth.BootstrapInput{
		Email: "owner@rot.test", DisplayName: "Owner", Password: "StrongPassword!123", OrgSlug: "rot-org", OrgName: "Rot Org",
	})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	svc, _ := st.CreateService(t.Context(), boot.OrgID, boot.EnvID, "echo", "Echo", "")
	upPool, _ := st.CreateUpstreamPool(t.Context(), store.UpstreamPool{OrgID: boot.OrgID, EnvID: boot.EnvID, Name: "p1", DestinationClass: "mock_local"})
	_, _ = st.AddUpstreamTarget(t.Context(), boot.OrgID, upPool.ID, "http://mock-upstream:8089", 100, true)
	_, _ = st.CreateRoute(t.Context(), store.Route{
		OrgID: boot.OrgID, EnvID: boot.EnvID, ServiceID: svc.ID, UpstreamPoolID: upPool.ID,
		Name: "r1", Host: "api.local", PathPrefix: "/echo", Methods: []string{"GET"}, Priority: 100, Enabled: true,
	})

	const syncTok = "rot-sync-token-123456"
	authH := &api.AuthHandler{Bootstrap: bootstrap, Sessions: sessions}
	consH := &api.ConsumerHandler{Store: st, Sessions: sessions}
	pubH := &api.PublicationHandler{Store: st, Sessions: sessions, TargetPolicy: netsec.DefaultTargetPolicy(true)}
	syncH := &api.SyncHandler{Store: st, SyncTokens: []string{syncTok}}

	router := api.NewRouter(api.RouterOptions{
		Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
		MountV1: func(v1 chi.Router) {
			authH.MountAuthRoutes(v1)
			consH.MountConsumerRoutes(v1)
			pubH.MountPublicationRoutes(v1)
			syncH.MountGatewaySyncRoutes(v1)
		},
	})

	loginRec := httptest.NewRecorder()
	router.ServeHTTP(loginRec, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{"email":"owner@rot.test","password":"StrongPassword!123"}`)))
	cookie := loginRec.Result().Cookies()[0]
	var loginOut struct {
		CSRFToken string `json:"csrf_token"`
	}
	_ = json.NewDecoder(loginRec.Body).Decode(&loginOut)

	post := func(path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
		req.AddCookie(cookie)
		req.Header.Set(auth.CSRFHeaderName, loginOut.CSRFToken)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	// Create consumer, issue key1, and publish v1.
	cRec := post("/api/v1/orgs/"+boot.OrgID+"/consumers", `{"name":"mobile-client"}`)
	var consumer api.Consumer
	_ = json.NewDecoder(cRec.Body).Decode(&consumer)

	k1Rec := post("/api/v1/orgs/"+boot.OrgID+"/consumers/"+consumer.ID+"/keys", `{"label":"v1-key","scopes":["orders:read"]}`)
	var k1 api.IssuedAPIKeyResponse
	_ = json.NewDecoder(k1Rec.Body).Decode(&k1)

	if pRec := post("/api/v1/orgs/"+boot.OrgID+"/publications", `{"notes":"v1"}`); pRec.Code != http.StatusCreated {
		t.Fatalf("publish v1 failed: %d %s", pRec.Code, pRec.Body.String())
	}

	fetchSnap := func() snapshot.Snapshot {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/gateway/snapshots/active", nil)
		req.Header.Set("Authorization", "Bearer "+syncTok)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("fetch snapshot failed: %d", rec.Code)
		}
		var snap snapshot.Snapshot
		_ = json.NewDecoder(rec.Body).Decode(&snap)
		return snap
	}

	// Active snapshot contains k1.
	snap1 := fetchSnap()
	if len(snap1.Consumers) != 1 || len(snap1.Consumers[0].Keys) != 1 || snap1.Consumers[0].Keys[0].KeyPrefix != k1.KeyPrefix {
		t.Fatalf("expected k1 in active snapshot, got %+v", snap1.Consumers)
	}

	// Rotate k1 -> k1 revoked, k2 issued atomically!
	rotRec := post("/api/v1/orgs/"+boot.OrgID+"/keys/"+k1.ID+"/rotate", `{}`)
	if rotRec.Code != http.StatusCreated {
		t.Fatalf("rotate expected 201, got %d: %s", rotRec.Code, rotRec.Body.String())
	}
	var rotOut struct {
		RevokedKeyID string                   `json:"revoked_key_id"`
		Replacement  api.IssuedAPIKeyResponse `json:"replacement"`
	}
	_ = json.NewDecoder(rotRec.Body).Decode(&rotOut)

	// Active snapshot IMMEDIATELY replaces k1 with k2!
	snap2 := fetchSnap()
	if len(snap2.Consumers[0].Keys) != 1 || snap2.Consumers[0].Keys[0].KeyPrefix != rotOut.Replacement.KeyPrefix {
		t.Fatalf("expected only replacement key in active snapshot, got %+v", snap2.Consumers[0].Keys)
	}

	// Revoke k2 -> active snapshot IMMEDIATELY has 0 keys!
	if revRec := post("/api/v1/orgs/"+boot.OrgID+"/keys/"+rotOut.Replacement.ID+"/revoke", `{}`); revRec.Code != http.StatusOK {
		t.Fatalf("revoke expected 200, got %d", revRec.Code)
	}
	snap3 := fetchSnap()
	if len(snap3.Consumers[0].Keys) != 0 {
		t.Fatalf("expected 0 active keys after revoke, got %+v", snap3.Consumers[0].Keys)
	}
}
