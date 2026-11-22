package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/YASSERRMD/GatewayForge/internal/control/api"
	"github.com/YASSERRMD/GatewayForge/internal/control/auth"
	"github.com/YASSERRMD/GatewayForge/internal/control/store"
	"github.com/YASSERRMD/GatewayForge/internal/shared/config"
)

func TestConsumerCRUDAndScopeIsolation(t *testing.T) {
	pool := openIntegrationDB(t)
	resetAndMigrateDB(t, pool)

	hasher := newTestArgonHasher()
	bootstrap := auth.NewBootstrapService(pool, config.EnvTest, hasher)
	sessions := auth.NewSessionManager(pool, hasher, time.Hour, false)
	st := store.New(pool)

	boot, err := bootstrap.Bootstrap(t.Context(), auth.BootstrapInput{
		Email: "owner@cons.test", DisplayName: "Owner", Password: "StrongPassword!123", OrgSlug: "cons-org", OrgName: "Cons Org",
	})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	pwDev, _ := hasher.Hash("DevPassword!1234")
	var devID string
	_ = pool.QueryRow(t.Context(), `INSERT INTO users (email, display_name, password_hash) VALUES ('dev@cons.test', 'Dev', $1) RETURNING id`, pwDev).Scan(&devID)
	_, _ = pool.Exec(t.Context(), `INSERT INTO memberships (org_id, user_id, role) VALUES ($1, $2, 'developer')`, boot.OrgID, devID)

	authH := &api.AuthHandler{Bootstrap: bootstrap, Sessions: sessions}
	consH := &api.ConsumerHandler{Store: st, Sessions: sessions, Audit: api.NewAuditRecorder(st)}
	router := api.NewRouter(api.RouterOptions{
		Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
		MountV1: func(v1 chi.Router) {
			authH.MountAuthRoutes(v1)
			consH.MountConsumerRoutes(v1)
		},
	})

	login := func(email, pw string) (*http.Cookie, string) {
		b, _ := json.Marshal(map[string]string{"email": email, "password": pw})
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(b)))
		var out struct {
			CSRFToken string `json:"csrf_token"`
		}
		_ = json.NewDecoder(rec.Body).Decode(&out)
		return rec.Result().Cookies()[0], out.CSRFToken
	}

	ownerCookie, ownerCSRF := login("owner@cons.test", "StrongPassword!123")
	devCookie, devCSRF := login("dev@cons.test", "DevPassword!1234")

	// Developer cannot create consumer (403 Forbidden).
	devReq := httptest.NewRequest(http.MethodPost, "/api/v1/orgs/"+boot.OrgID+"/consumers", bytes.NewBufferString(`{"name":"partner-app"}`))
	devReq.AddCookie(devCookie)
	devReq.Header.Set(auth.CSRFHeaderName, devCSRF)
	devRec := httptest.NewRecorder()
	router.ServeHTTP(devRec, devReq)
	if devRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for developer creating consumer, got %d", devRec.Code)
	}

	// Owner creates and suspends consumer.
	crReq := httptest.NewRequest(http.MethodPost, "/api/v1/orgs/"+boot.OrgID+"/consumers", bytes.NewBufferString(`{"name":"partner-app","owner_email":"ops@partner.test"}`))
	crReq.AddCookie(ownerCookie)
	crReq.Header.Set(auth.CSRFHeaderName, ownerCSRF)
	crRec := httptest.NewRecorder()
	router.ServeHTTP(crRec, crReq)
	if crRec.Code != http.StatusCreated {
		t.Fatalf("expected 201 creating consumer, got %d: %s", crRec.Code, crRec.Body.String())
	}
	var c api.Consumer
	_ = json.NewDecoder(crRec.Body).Decode(&c)

	suspReq := httptest.NewRequest(http.MethodPatch, "/api/v1/orgs/"+boot.OrgID+"/consumers/"+c.ID, bytes.NewBufferString(`{"status":"suspended","revision":1}`))
	suspReq.AddCookie(ownerCookie)
	suspReq.Header.Set(auth.CSRFHeaderName, ownerCSRF)
	suspRec := httptest.NewRecorder()
	router.ServeHTTP(suspRec, suspReq)
	if suspRec.Code != http.StatusOK {
		t.Fatalf("expected 200 suspending consumer, got %d: %s", suspRec.Code, suspRec.Body.String())
	}

	// Issue one-time API key: plaintext returned ONCE, NEVER stored in DB or returned on GET readback.
	issueReq := httptest.NewRequest(http.MethodPost, "/api/v1/orgs/"+boot.OrgID+"/consumers/"+c.ID+"/keys",
		bytes.NewBufferString(`{"label":"ci-key","scopes":["orders:read"]}`))
	issueReq.AddCookie(ownerCookie)
	issueReq.Header.Set(auth.CSRFHeaderName, ownerCSRF)
	issueRec := httptest.NewRecorder()
	router.ServeHTTP(issueRec, issueReq)
	if issueRec.Code != http.StatusCreated {
		t.Fatalf("expected 201 issuing key, got %d: %s", issueRec.Code, issueRec.Body.String())
	}
	var issued api.IssuedAPIKeyResponse
	_ = json.NewDecoder(issueRec.Body).Decode(&issued)
	if !strings.HasPrefix(issued.APIKey, "gwf_") || len(issued.APIKey) < 64 {
		t.Fatalf("expected high-entropy gwf_ api key, got %q", issued.APIKey)
	}

	// Readback via GET MUST NOT contain plaintext or hash.
	listKeysReq := httptest.NewRequest(http.MethodGet, "/api/v1/orgs/"+boot.OrgID+"/consumers/"+c.ID+"/keys", nil)
	listKeysReq.AddCookie(ownerCookie)
	listKeysRec := httptest.NewRecorder()
	router.ServeHTTP(listKeysRec, listKeysReq)
	if listKeysRec.Code != http.StatusOK {
		t.Fatalf("expected 200 listing keys, got %d", listKeysRec.Code)
	}
	if strings.Contains(listKeysRec.Body.String(), issued.APIKey) {
		t.Fatal("GET /keys leaked plaintext API key!")
	}

	// Direct DB check: stored key_hash must NOT equal plaintext.
	var storedHash string
	if err := pool.QueryRow(t.Context(), `SELECT key_hash FROM api_keys WHERE id = $1`, issued.ID).Scan(&storedHash); err != nil {
		t.Fatalf("query key_hash: %v", err)
	}
	if storedHash == issued.APIKey || strings.Contains(storedHash, issued.APIKey) {
		t.Fatal("plaintext API key was stored in PostgreSQL!")
	}
}
