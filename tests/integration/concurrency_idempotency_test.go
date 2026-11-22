package integration

import (
	"bytes"
	"encoding/json"
	"errors"
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
)

func TestOptimisticConcurrencyAndIdempotency(t *testing.T) {
	pool := openIntegrationDB(t)
	resetAndMigrateDB(t, pool)

	hasher := newTestArgonHasher()
	bootstrap := auth.NewBootstrapService(pool, config.EnvTest, hasher)
	sessions := auth.NewSessionManager(pool, hasher, time.Hour, false)
	st := store.New(pool)

	boot, err := bootstrap.Bootstrap(t.Context(), auth.BootstrapInput{
		Email: "owner@conc.test", DisplayName: "Owner", Password: "StrongPassword!123", OrgSlug: "conc-org", OrgName: "Conc Org",
	})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	// 1. IdempotencyStore replay & payload-mismatch detection.
	idem := api.NewIdempotencyStore(pool)
	body1 := []byte(`{"name":"orders"}`)
	hash1 := api.HashRequestPayload("POST", "/api/v1/orgs/"+boot.OrgID+"/services", body1)
	if _, hit, err := idem.Lookup(t.Context(), boot.OrgID, "idem-key-12345678", hash1); err != nil || hit {
		t.Fatalf("expected cache miss on fresh key, got hit=%v err=%v", hit, err)
	}
	if err := idem.Save(t.Context(), boot.OrgID, "idem-key-12345678", "POST", "/api/v1/orgs/"+boot.OrgID+"/services", hash1, 201, json.RawMessage(`{"id":"svc-1"}`)); err != nil {
		t.Fatalf("save idempotency key: %v", err)
	}
	rec, hit, err := idem.Lookup(t.Context(), boot.OrgID, "idem-key-12345678", hash1)
	if err != nil || !hit || rec.ResponseStatus != 201 {
		t.Fatalf("expected exact replay hit=true status=201, got hit=%v rec=%+v err=%v", hit, rec, err)
	}
	hash2 := api.HashRequestPayload("POST", "/api/v1/orgs/"+boot.OrgID+"/services", []byte(`{"name":"different"}`))
	if _, _, err := idem.Lookup(t.Context(), boot.OrgID, "idem-key-12345678", hash2); !errors.Is(err, api.ErrIdempotencyPayloadMismatch) {
		t.Fatalf("expected ErrIdempotencyPayloadMismatch, got: %v", err)
	}

	// 2. Concurrent PATCH against same org revision=1: exactly 1 succeeds, rest get 409 Conflict.
	authH := &api.AuthHandler{Bootstrap: bootstrap, Sessions: sessions}
	orgH := &api.OrgHandler{Store: st, Sessions: sessions}
	router := api.NewRouter(api.RouterOptions{
		Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
		MountV1: func(v1 chi.Router) {
			authH.MountAuthRoutes(v1)
			orgH.MountOrgRoutes(v1)
		},
	})

	loginRec := httptest.NewRecorder()
	router.ServeHTTP(loginRec, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{"email":"owner@conc.test","password":"StrongPassword!123"}`)))
	cookie := loginRec.Result().Cookies()[0]
	var loginOut struct {
		CSRFToken string `json:"csrf_token"`
	}
	_ = json.NewDecoder(loginRec.Body).Decode(&loginOut)

	const workers = 6
	var okCount, conflictCount atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			req := httptest.NewRequest(http.MethodPatch, "/api/v1/orgs/"+boot.OrgID, bytes.NewBufferString(`{"display_name":"Updated Race"}`))
			req.AddCookie(cookie)
			req.Header.Set(auth.CSRFHeaderName, loginOut.CSRFToken)
			req.Header.Set("If-Match", `W/"rev-1"`)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			switch w.Code {
			case http.StatusOK:
				okCount.Add(1)
			case http.StatusConflict:
				conflictCount.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()

	if okCount.Load() != 1 || conflictCount.Load() != workers-1 {
		t.Fatalf("expected 1 OK and %d Conflict, got ok=%d conflict=%d", workers-1, okCount.Load(), conflictCount.Load())
	}
}
