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
	"github.com/YASSERRMD/GatewayForge/internal/control/rbac"
	"github.com/YASSERRMD/GatewayForge/internal/control/store"
	"github.com/YASSERRMD/GatewayForge/internal/shared/config"
)

func TestAuditMiddlewareRedactsSecretsAndFilters(t *testing.T) {
	pool := openIntegrationDB(t)
	resetAndMigrateDB(t, pool)

	hasher := newTestArgonHasher()
	bootstrap := auth.NewBootstrapService(pool, config.EnvTest, hasher)
	sessions := auth.NewSessionManager(pool, hasher, time.Hour, false)
	st := store.New(pool)
	recorder := api.NewAuditRecorder(st)

	boot, err := bootstrap.Bootstrap(t.Context(), auth.BootstrapInput{
		Email: "owner@audit.test", DisplayName: "Owner", Password: "StrongPassword!123", OrgSlug: "audit-org", OrgName: "Audit Org",
	})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	authH := &api.AuthHandler{Bootstrap: bootstrap, Sessions: sessions}
	memH := &api.MembershipHandler{Store: st, Sessions: sessions, Hasher: hasher}
	auditH := &api.AuditHandler{Store: st, Sessions: sessions}

	router := api.NewRouter(api.RouterOptions{
		Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
		MountV1: func(v1 chi.Router) {
			authH.MountAuthRoutes(v1)
			v1.With(api.RequireAuth(sessions), api.RequireOrgContext(sessions), api.AuditMutationsMiddleware(recorder)).
				Route("/orgs/{orgId}/members-audited", func(r chi.Router) {
					r.With(api.RequirePermission(rbac.PermMemberWrite)).Post("/", func(w http.ResponseWriter, req *http.Request) {
						// Delegate to MembershipHandler via subrouter pattern
						memH.MountMembershipRoutes(chi.NewRouter())
						api.WriteJSON(w, http.StatusCreated, map[string]string{"status": "ok"})
					})
				})
			auditH.MountAuditRoutes(v1)
		},
	})

	loginRec := httptest.NewRecorder()
	router.ServeHTTP(loginRec, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{"email":"owner@audit.test","password":"StrongPassword!123"}`)))
	cookie := loginRec.Result().Cookies()[0]
	var loginOut struct {
		CSRFToken string `json:"csrf_token"`
	}
	_ = json.NewDecoder(loginRec.Body).Decode(&loginOut)

	// Send mutation containing sensitive password & api_key fields.
	secretValue := "UltraSecretPassword!987654"
	mutReq := httptest.NewRequest(http.MethodPost, "/api/v1/orgs/"+boot.OrgID+"/members-audited",
		bytes.NewBufferString(`{"email":"dev@audit.test","password":"`+secretValue+`","api_key":"gwf_live_dontleak"}`))
	mutReq.AddCookie(cookie)
	mutReq.Header.Set(auth.CSRFHeaderName, loginOut.CSRFToken)
	mutReq.Header.Set("X-Request-Id", "req-audit-proof-1")
	mutRec := httptest.NewRecorder()
	router.ServeHTTP(mutRec, mutReq)
	if mutRec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", mutRec.Code, mutRec.Body.String())
	}

	// Query audit events via API with target_type filter.
	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/orgs/"+boot.OrgID+"/audit-events?target_type=members-audited", nil)
	listReq.AddCookie(cookie)
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected 200 querying audit-events, got %d: %s", listRec.Code, listRec.Body.String())
	}

	bodyStr := listRec.Body.String()
	if strings.Contains(bodyStr, secretValue) || strings.Contains(bodyStr, "gwf_live_dontleak") {
		t.Fatalf("audit event leaked sensitive field: %s", bodyStr)
	}
	if !strings.Contains(bodyStr, `"password":"[REDACTED]"`) || !strings.Contains(bodyStr, "req-audit-proof-1") {
		t.Fatalf("expected redacted password and request_id in audit event: %s", bodyStr)
	}
}
