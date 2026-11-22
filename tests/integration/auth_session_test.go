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
	"github.com/YASSERRMD/GatewayForge/internal/shared/config"
)

func TestAuthLoginLogoutExpiryAndCSRF(t *testing.T) {
	pool := openIntegrationDB(t)
	resetAndMigrateDB(t, pool)

	hasher := newTestArgonHasher()
	bootstrap := auth.NewBootstrapService(pool, config.EnvTest, hasher)
	now := time.Now().UTC()
	sessions := auth.NewSessionManager(pool, hasher, 1*time.Hour, false)
	sessions.SetClock(func() time.Time { return now })

	authHandler := &api.AuthHandler{Bootstrap: bootstrap, Sessions: sessions}
	router := api.NewRouter(api.RouterOptions{
		Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
		MountV1: func(v1 chi.Router) {
			authHandler.MountAuthRoutes(v1)
		},
	})

	// 1. Bootstrap first admin.
	bootBody := `{"email":"owner@session.test","display_name":"Owner","password":"SuperStrongPassword!123","org_slug":"sess-org","org_name":"Session Org"}`
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/auth/bootstrap", bytes.NewBufferString(bootBody)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("bootstrap expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	// 2. Wrong password returns 401.
	badLogin := httptest.NewRecorder()
	router.ServeHTTP(badLogin, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{"email":"owner@session.test","password":"WrongPassword!123"}`)))
	if badLogin.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for wrong password, got %d", badLogin.Code)
	}

	// 3. Valid login issues HttpOnly session cookie + CSRF token.
	loginRec := httptest.NewRecorder()
	router.ServeHTTP(loginRec, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{"email":"owner@session.test","password":"SuperStrongPassword!123"}`)))
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login expected 200, got %d: %s", loginRec.Code, loginRec.Body.String())
	}
	var sessionCookie *http.Cookie
	for _, c := range loginRec.Result().Cookies() {
		if c.Name == auth.SessionCookieName {
			sessionCookie = c
		}
	}
	if sessionCookie == nil || !sessionCookie.HttpOnly || sessionCookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("expected HttpOnly SameSite=Lax session cookie, got: %+v", sessionCookie)
	}
	var loginPayload struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.NewDecoder(loginRec.Body).Decode(&loginPayload); err != nil || loginPayload.CSRFToken == "" {
		t.Fatalf("expected csrf_token in login response, got err=%v payload=%+v", err, loginPayload)
	}

	// 4. Logout without CSRF header MUST fail with 403.
	noCSRFReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	noCSRFReq.AddCookie(sessionCookie)
	noCSRFRec := httptest.NewRecorder()
	router.ServeHTTP(noCSRFRec, noCSRFReq)
	if noCSRFRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without CSRF token, got %d", noCSRFRec.Code)
	}

	// 5. Logout with valid CSRF token succeeds and revokes session server-side.
	logoutReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	logoutReq.AddCookie(sessionCookie)
	logoutReq.Header.Set(auth.CSRFHeaderName, loginPayload.CSRFToken)
	logoutRec := httptest.NewRecorder()
	router.ServeHTTP(logoutRec, logoutReq)
	if logoutRec.Code != http.StatusOK {
		t.Fatalf("expected 200 on logout, got %d: %s", logoutRec.Code, logoutRec.Body.String())
	}

	// Reusing revoked cookie MUST fail with 401.
	replayReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	replayReq.AddCookie(sessionCookie)
	replayReq.Header.Set(auth.CSRFHeaderName, loginPayload.CSRFToken)
	replayRec := httptest.NewRecorder()
	router.ServeHTTP(replayRec, replayReq)
	if replayRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for revoked session replay, got %d", replayRec.Code)
	}

	// 6. Expiry check: advance clock past TTL and verify Authenticate rejects.
	raw2, _, err := sessions.Login(t.Context(), "owner@session.test", "SuperStrongPassword!123")
	if err != nil {
		t.Fatalf("second login: %v", err)
	}
	now = now.Add(2 * time.Hour)
	if _, err := sessions.Authenticate(t.Context(), raw2); err == nil {
		t.Fatal("expected expired session to be rejected")
	}
}
