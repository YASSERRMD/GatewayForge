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
)

func TestUpstreamAPIValidationAndSSRFProtection(t *testing.T) {
	pool := openIntegrationDB(t)
	resetAndMigrateDB(t, pool)

	hasher := newTestArgonHasher()
	bootstrap := auth.NewBootstrapService(pool, config.EnvTest, hasher)
	sessions := auth.NewSessionManager(pool, hasher, time.Hour, false)
	st := store.New(pool)

	boot, err := bootstrap.Bootstrap(t.Context(), auth.BootstrapInput{
		Email: "owner@up.test", DisplayName: "Owner", Password: "StrongPassword!123", OrgSlug: "up-org", OrgName: "Up Org",
	})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	authH := &api.AuthHandler{Bootstrap: bootstrap, Sessions: sessions}
	upH := &api.UpstreamHandler{
		Store:        st,
		Sessions:     sessions,
		Audit:        api.NewAuditRecorder(st),
		TargetPolicy: netsec.DefaultTargetPolicy(true),
	}
	router := api.NewRouter(api.RouterOptions{
		Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
		MountV1: func(v1 chi.Router) {
			authH.MountAuthRoutes(v1)
			upH.MountUpstreamRoutes(v1)
		},
	})

	loginRec := httptest.NewRecorder()
	router.ServeHTTP(loginRec, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{"email":"owner@up.test","password":"StrongPassword!123"}`)))
	cookie := loginRec.Result().Cookies()[0]
	var loginOut struct {
		CSRFToken string `json:"csrf_token"`
	}
	_ = json.NewDecoder(loginRec.Body).Decode(&loginOut)

	// 1. Metadata SSRF target MUST be rejected with 422.
	ssrfReq := httptest.NewRequest(http.MethodPost, "/api/v1/orgs/"+boot.OrgID+"/upstreams",
		bytes.NewBufferString(`{"name":"bad-ssrf","destination_class":"public","targets":[{"url":"http://169.254.169.254/latest/meta-data","weight":100}]}`))
	ssrfReq.AddCookie(cookie)
	ssrfReq.Header.Set(auth.CSRFHeaderName, loginOut.CSRFToken)
	ssrfRec := httptest.NewRecorder()
	router.ServeHTTP(ssrfRec, ssrfReq)
	if ssrfRec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 rejecting metadata IP target, got %d: %s", ssrfRec.Code, ssrfRec.Body.String())
	}

	// 2. Approved mock-upstream pool with weighted target succeeds (201).
	okReq := httptest.NewRequest(http.MethodPost, "/api/v1/orgs/"+boot.OrgID+"/upstreams",
		bytes.NewBufferString(`{"name":"echo-pool","destination_class":"mock_local","connect_timeout_ms":2000,"read_timeout_ms":8000,"targets":[{"url":"http://mock-upstream:8089","weight":100}]}`))
	okReq.AddCookie(cookie)
	okReq.Header.Set(auth.CSRFHeaderName, loginOut.CSRFToken)
	okRec := httptest.NewRecorder()
	router.ServeHTTP(okRec, okReq)
	if okRec.Code != http.StatusCreated {
		t.Fatalf("expected 201 creating valid mock pool, got %d: %s", okRec.Code, okRec.Body.String())
	}
	var created struct {
		ID      string                 `json:"id"`
		Targets []store.UpstreamTarget `json:"targets"`
	}
	_ = json.NewDecoder(okRec.Body).Decode(&created)
	if created.ID == "" || len(created.Targets) != 1 {
		t.Fatalf("unexpected created pool: %+v", created)
	}

	// 3. Adding private raw IP target to existing pool MUST fail with 422.
	privReq := httptest.NewRequest(http.MethodPost, "/api/v1/orgs/"+boot.OrgID+"/upstreams/"+created.ID+"/targets",
		bytes.NewBufferString(`{"url":"http://10.0.0.1:8080","weight":50}`))
	privReq.AddCookie(cookie)
	privReq.Header.Set(auth.CSRFHeaderName, loginOut.CSRFToken)
	privRec := httptest.NewRecorder()
	router.ServeHTTP(privRec, privReq)
	if privRec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 rejecting raw private IP target, got %d", privRec.Code)
	}
}
