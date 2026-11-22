package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
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

func TestRouteAPIOrderingConflictsAndCRUD(t *testing.T) {
	pool := openIntegrationDB(t)
	resetAndMigrateDB(t, pool)

	hasher := newTestArgonHasher()
	bootstrap := auth.NewBootstrapService(pool, config.EnvTest, hasher)
	sessions := auth.NewSessionManager(pool, hasher, time.Hour, false)
	st := store.New(pool)
	auditRec := api.NewAuditRecorder(st)

	boot, err := bootstrap.Bootstrap(t.Context(), auth.BootstrapInput{
		Email: "owner@rt.test", DisplayName: "Owner", Password: "StrongPassword!123", OrgSlug: "rt-org", OrgName: "RT Org",
	})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	svc, err := st.CreateService(t.Context(), boot.OrgID, boot.EnvID, "echo", "Echo", "")
	if err != nil {
		t.Fatalf("create service: %v", err)
	}
	upPool, err := st.CreateUpstreamPool(t.Context(), store.UpstreamPool{
		OrgID: boot.OrgID, EnvID: boot.EnvID, Name: "echo-pool", DestinationClass: "mock_local",
	})
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}

	authH := &api.AuthHandler{Bootstrap: bootstrap, Sessions: sessions}
	rtH := &api.RouteHandler{Store: st, Sessions: sessions, Audit: auditRec}
	upH := &api.UpstreamHandler{Store: st, Sessions: sessions, TargetPolicy: netsec.DefaultTargetPolicy(true)}
	router := api.NewRouter(api.RouterOptions{
		Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
		MountV1: func(v1 chi.Router) {
			authH.MountAuthRoutes(v1)
			upH.MountUpstreamRoutes(v1)
			rtH.MountRouteRoutes(v1)
		},
	})

	loginRec := httptest.NewRecorder()
	router.ServeHTTP(loginRec, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{"email":"owner@rt.test","password":"StrongPassword!123"}`)))
	cookie := loginRec.Result().Cookies()[0]
	var loginOut struct {
		CSRFToken string `json:"csrf_token"`
	}
	_ = json.NewDecoder(loginRec.Body).Decode(&loginOut)

	postRoute := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/orgs/"+boot.OrgID+"/routes", bytes.NewBufferString(body))
		req.AddCookie(cookie)
		req.Header.Set(auth.CSRFHeaderName, loginOut.CSRFToken)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	// 1. Create two routes with different specificity / priority.
	r1 := postRoute(fmt.Sprintf(`{"service_id":"%s","upstream_pool_id":"%s","name":"base-api","host":"api.local","path_prefix":"/api","methods":["GET"],"priority":100}`, svc.ID, upPool.ID))
	if r1.Code != http.StatusCreated {
		t.Fatalf("r1 expected 201, got %d: %s", r1.Code, r1.Body.String())
	}
	r2 := postRoute(fmt.Sprintf(`{"service_id":"%s","upstream_pool_id":"%s","name":"orders-api","host":"api.local","path_prefix":"/api/orders","methods":["GET","POST"],"priority":100}`, svc.ID, upPool.ID))
	if r2.Code != http.StatusCreated {
		t.Fatalf("r2 expected 201, got %d: %s", r2.Code, r2.Body.String())
	}

	// 2. Overlapping conflict at identical (host, path_prefix, priority) MUST return 409.
	conflict := postRoute(fmt.Sprintf(`{"service_id":"%s","upstream_pool_id":"%s","name":"orders-dup","host":"api.local","path_prefix":"/api/orders","methods":["POST"],"priority":100}`, svc.ID, upPool.ID))
	if conflict.Code != http.StatusConflict {
		t.Fatalf("expected 409 on overlapping route conflict, got %d: %s", conflict.Code, conflict.Body.String())
	}

	// 3. Invalid path traversal prefix MUST return 422.
	bad := postRoute(fmt.Sprintf(`{"service_id":"%s","upstream_pool_id":"%s","name":"bad","host":"api.local","path_prefix":"/api/../secret","methods":["GET"]}`, svc.ID, upPool.ID))
	if bad.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 on traversal path_prefix, got %d", bad.Code)
	}

	// 4. Precedence preview orders /api/orders BEFORE /api.
	prevReq := httptest.NewRequest(http.MethodGet, "/api/v1/orgs/"+boot.OrgID+"/routes/preview", nil)
	prevReq.AddCookie(cookie)
	prevRec := httptest.NewRecorder()
	router.ServeHTTP(prevRec, prevReq)
	var prev struct {
		OrderedRoutes []store.Route `json:"ordered_routes"`
	}
	_ = json.NewDecoder(prevRec.Body).Decode(&prev)
	if prevRec.Code != http.StatusOK || len(prev.OrderedRoutes) != 2 || prev.OrderedRoutes[0].Name != "orders-api" {
		t.Fatalf("unexpected preview ordering: code=%d routes=%+v", prevRec.Code, prev.OrderedRoutes)
	}
}
