package integration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/YASSERRMD/GatewayForge/internal/control/api"
	"github.com/YASSERRMD/GatewayForge/internal/control/auth"
	"github.com/YASSERRMD/GatewayForge/internal/control/store"
	"github.com/YASSERRMD/GatewayForge/internal/shared/netsec"
)

func TestConcurrentReleaseActivationOptimisticLock(t *testing.T) {
	pool := openIntegrationDB(t)
	defer pool.Close()
	resetAndMigrateDB(t, pool)
	st := store.New(pool)

	hasher := newTestArgonHasher()
	bootstrapSvc := auth.NewBootstrapService(pool, "test", hasher)
	sessMgr := auth.NewSessionManager(pool, hasher, time.Hour, false)
	auditRec := api.NewAuditRecorder(st)
	targetPol := netsec.DefaultTargetPolicy(true)

	authH := &api.AuthHandler{Bootstrap: bootstrapSvc, Sessions: sessMgr}
	pubH := &api.PublicationHandler{Store: st, Sessions: sessMgr, Audit: auditRec, TargetPolicy: targetPol}

	router := api.NewRouter(api.RouterOptions{
		MountV1: func(v1 chi.Router) {
			authH.MountAuthRoutes(v1)
			pubH.MountPublicationRoutes(v1)
		},
	})
	ts := httptest.NewServer(router)
	defer ts.Close()

	res, body := doOAJSON(t, http.MethodPost, ts.URL+"/api/v1/auth/bootstrap", map[string]any{
		"email":        "act-admin@acme.test",
		"password":     "SuperSecurePass!2026",
		"display_name": "Act Admin",
		"org_name":     "Act Org",
		"org_slug":     "act-org",
	}, nil, "")
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("bootstrap failed: %d %s", res.StatusCode, string(body))
	}

	loginRes, loginBody := doOAJSON(t, http.MethodPost, ts.URL+"/api/v1/auth/login", map[string]any{
		"email":    "act-admin@acme.test",
		"password": "SuperSecurePass!2026",
	}, nil, "")
	cookies := loginRes.Cookies()
	var loginOut struct {
		CSRFToken string `json:"csrf_token"`
	}
	_ = json.Unmarshal(loginBody, &loginOut)
	_, meBody := doOAJSON(t, http.MethodGet, ts.URL+"/api/v1/auth/me", nil, cookies, "")
	var meOut struct {
		Memberships []struct {
			OrgID string `json:"org_id"`
		} `json:"memberships"`
	}
	_ = json.Unmarshal(meBody, &meOut)
	orgID := meOut.Memberships[0].OrgID

	// Publish v1 (active, activation_revision = 1)
	p1Res, p1Body := doOAJSON(t, http.MethodPost, ts.URL+"/api/v1/orgs/"+orgID+"/publications", map[string]any{
		"notes":    "v1",
		"activate": true,
	}, cookies, loginOut.CSRFToken)
	if p1Res.StatusCode != http.StatusCreated {
		t.Fatalf("publish v1 failed: %d %s", p1Res.StatusCode, string(p1Body))
	}

	// Publish v2 (inactive)
	p2Res, p2Body := doOAJSON(t, http.MethodPost, ts.URL+"/api/v1/orgs/"+orgID+"/publications", map[string]any{
		"notes":    "v2 staged",
		"activate": false,
	}, cookies, loginOut.CSRFToken)
	if p2Res.StatusCode != http.StatusCreated {
		t.Fatalf("publish v2 failed: %d %s", p2Res.StatusCode, string(p2Body))
	}

	// Concurrently activate v2 from two racing operators expecting activation_revision = 1
	var wg sync.WaitGroup
	statuses := make([]int, 2)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			r, _ := doOAJSON(t, http.MethodPost, ts.URL+"/api/v1/orgs/"+orgID+"/publications/2/activate", map[string]any{
				"expected_activation_revision": 1,
				"reason":                       "concurrent activation race",
			}, cookies, loginOut.CSRFToken)
			statuses[idx] = r.StatusCode
		}(i)
	}
	close(start)
	wg.Wait()

	okCount, conflictCount := 0, 0
	for _, stCode := range statuses {
		switch stCode {
		case http.StatusOK:
			okCount++
		case http.StatusConflict:
			conflictCount++
		}
	}
	if okCount != 1 || conflictCount != 1 {
		t.Fatalf("expected exactly one 200 and one 409 under concurrent activation, got %v", statuses)
	}
}
