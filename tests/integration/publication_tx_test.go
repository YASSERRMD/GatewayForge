package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/YASSERRMD/GatewayForge/internal/control/api"
	"github.com/YASSERRMD/GatewayForge/internal/control/auth"
	"github.com/YASSERRMD/GatewayForge/internal/control/store"
	"github.com/YASSERRMD/GatewayForge/internal/shared/config"
	"github.com/YASSERRMD/GatewayForge/internal/shared/netsec"
)

func TestPublicationTransactionConcurrencyAndRollback(t *testing.T) {
	pool := openIntegrationDB(t)
	resetAndMigrateDB(t, pool)

	hasher := newTestArgonHasher()
	bootstrap := auth.NewBootstrapService(pool, config.EnvTest, hasher)
	sessions := auth.NewSessionManager(pool, hasher, time.Hour, false)
	st := store.New(pool)

	boot, err := bootstrap.Bootstrap(t.Context(), auth.BootstrapInput{
		Email: "owner@pub.test", DisplayName: "Owner", Password: "StrongPassword!123", OrgSlug: "pub-org", OrgName: "Pub Org",
	})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	svc, _ := st.CreateService(t.Context(), boot.OrgID, boot.EnvID, "echo", "Echo", "")
	upPool, _ := st.CreateUpstreamPool(t.Context(), store.UpstreamPool{
		OrgID: boot.OrgID, EnvID: boot.EnvID, Name: "echo-pool", DestinationClass: "mock_local",
	})
	_, _ = st.CreateRoute(t.Context(), store.Route{
		OrgID: boot.OrgID, EnvID: boot.EnvID, ServiceID: svc.ID, UpstreamPoolID: upPool.ID,
		Name: "echo-v1", Host: "api.local", PathPrefix: "/echo", Methods: []string{"GET"}, Priority: 100, Enabled: true,
	})

	authH := &api.AuthHandler{Bootstrap: bootstrap, Sessions: sessions}
	pubH := &api.PublicationHandler{
		Store:        st,
		Sessions:     sessions,
		Audit:        api.NewAuditRecorder(st),
		TargetPolicy: netsec.DefaultTargetPolicy(true),
	}
	router := api.NewRouter(api.RouterOptions{
		Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
		MountV1: func(v1 chi.Router) {
			authH.MountAuthRoutes(v1)
			pubH.MountPublicationRoutes(v1)
		},
	})

	loginRec := httptest.NewRecorder()
	router.ServeHTTP(loginRec, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{"email":"owner@pub.test","password":"StrongPassword!123"}`)))
	cookie := loginRec.Result().Cookies()[0]
	var loginOut struct {
		CSRFToken string `json:"csrf_token"`
	}
	_ = json.NewDecoder(loginRec.Body).Decode(&loginOut)

	// 1. Publishing while pool has zero enabled targets MUST fail with 422.
	badPub := httptest.NewRequest(http.MethodPost, "/api/v1/orgs/"+boot.OrgID+"/publications", bytes.NewBufferString(`{"notes":"should fail"}`))
	badPub.AddCookie(cookie)
	badPub.Header.Set(auth.CSRFHeaderName, loginOut.CSRFToken)
	badRec := httptest.NewRecorder()
	router.ServeHTTP(badRec, badPub)
	if badRec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 publishing empty pool draft, got %d: %s", badRec.Code, badRec.Body.String())
	}

	// Add valid target so draft becomes valid.
	if _, err := st.AddUpstreamTarget(t.Context(), boot.OrgID, upPool.ID, "http://mock-upstream:8089", 100, true); err != nil {
		t.Fatalf("add target: %v", err)
	}

	// 2. Concurrent publishers supplying the SAME expected_draft_revision=1:
	// exactly ONE must succeed (201, version 1), the rest MUST get 409 Conflict!
	const workers = 6
	var createdCount, conflictCount atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			req := httptest.NewRequest(http.MethodPost, "/api/v1/orgs/"+boot.OrgID+"/publications",
				bytes.NewBufferString(`{"expected_draft_revision":1,"notes":"race v1"}`))
			req.AddCookie(cookie)
			req.Header.Set(auth.CSRFHeaderName, loginOut.CSRFToken)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			switch rec.Code {
			case http.StatusCreated:
				createdCount.Add(1)
			case http.StatusConflict:
				conflictCount.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if createdCount.Load() != 1 || conflictCount.Load() != workers-1 {
		t.Fatalf("optimistic publish race violated: created=%d conflict=%d", createdCount.Load(), conflictCount.Load())
	}

	// 3. Publish v2, then roll back to v1 via POST /publications/1/activate.
	pubV2 := httptest.NewRequest(http.MethodPost, "/api/v1/orgs/"+boot.OrgID+"/publications", bytes.NewBufferString(`{"notes":"release v2"}`))
	pubV2.AddCookie(cookie)
	pubV2.Header.Set(auth.CSRFHeaderName, loginOut.CSRFToken)
	recV2 := httptest.NewRecorder()
	router.ServeHTTP(recV2, pubV2)
	if recV2.Code != http.StatusCreated {
		t.Fatalf("expected 201 for v2, got %d: %s", recV2.Code, recV2.Body.String())
	}

	rbReq := httptest.NewRequest(http.MethodPost, "/api/v1/orgs/"+boot.OrgID+"/publications/1/activate", bytes.NewBufferString(`{"reason":"rollback to v1"}`))
	rbReq.AddCookie(cookie)
	rbReq.Header.Set(auth.CSRFHeaderName, loginOut.CSRFToken)
	rbRec := httptest.NewRecorder()
	router.ServeHTTP(rbRec, rbReq)
	if rbRec.Code != http.StatusOK {
		t.Fatalf("expected 200 rolling back to v1, got %d: %s", rbRec.Code, rbRec.Body.String())
	}
	var rbOut struct {
		Version    int  `json:"version"`
		IsRollback bool `json:"is_rollback"`
	}
	_ = json.NewDecoder(rbRec.Body).Decode(&rbOut)
	if rbOut.Version != 1 || !rbOut.IsRollback {
		t.Fatalf("expected version=1 is_rollback=true, got %+v", rbOut)
	}
}
