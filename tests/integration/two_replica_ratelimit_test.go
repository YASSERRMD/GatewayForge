package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/YASSERRMD/GatewayForge/internal/control/api"
	"github.com/YASSERRMD/GatewayForge/internal/control/auth"
	"github.com/YASSERRMD/GatewayForge/internal/control/store"
	"github.com/YASSERRMD/GatewayForge/internal/gateway"
	"github.com/YASSERRMD/GatewayForge/internal/gateway/ratelimit"
	syncer "github.com/YASSERRMD/GatewayForge/internal/gateway/sync"
	"github.com/YASSERRMD/GatewayForge/internal/shared/cache"
	"github.com/YASSERRMD/GatewayForge/internal/shared/config"
	"github.com/YASSERRMD/GatewayForge/internal/shared/events"
	"github.com/YASSERRMD/GatewayForge/internal/shared/netsec"
)

func TestTwoReplicaGlobalRateLimitConsistencyAndFailModes(t *testing.T) {
	pool := openIntegrationDB(t)
	resetAndMigrateDB(t, pool)

	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "127.0.0.1:6389"
	}
	redisPw := os.Getenv("REDIS_PASSWORD")
	if redisPw == "" {
		redisPw = "gatewayforge_local_redis_pw"
	}
	mockURL := os.Getenv("MOCK_UPSTREAM_URL")
	if mockURL == "" {
		mockURL = "http://127.0.0.1:18089"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	rc, err := cache.Connect(ctx, redisAddr, redisPw)
	if err != nil {
		t.Fatalf("connect redis: %v", err)
	}
	defer func() { _ = rc.Close() }()

	silent := slog.New(slog.NewJSONHandler(io.Discard, nil))
	channel := fmt.Sprintf("gatewayforge:test:rl-consistency:%d", time.Now().UnixNano())
	eventPub := events.NewPublisher(rc, channel)

	hasher := newTestArgonHasher()
	bootstrap := auth.NewBootstrapService(pool, config.EnvTest, hasher)
	sessions := auth.NewSessionManager(pool, hasher, time.Hour, false)
	st := store.New(pool)
	auditRec := api.NewAuditRecorder(st)
	targetPol := netsec.DefaultTargetPolicy(true)
	targetPol.AllowLoopback = true

	const syncToken = "rl-consistency-sync-token-12345"
	authH := &api.AuthHandler{Bootstrap: bootstrap, Sessions: sessions}
	svcH := &api.ServiceHandler{Store: st, Sessions: sessions, Audit: auditRec}
	upH := &api.UpstreamHandler{Store: st, Sessions: sessions, Audit: auditRec, TargetPolicy: targetPol}
	rtH := &api.RouteHandler{Store: st, Sessions: sessions, Audit: auditRec}
	consH := &api.ConsumerHandler{Store: st, Sessions: sessions, Audit: auditRec}
	polH := &api.PolicyHandler{Store: st, Sessions: sessions, Audit: auditRec}
	pubH := &api.PublicationHandler{
		Store:        st,
		Sessions:     sessions,
		Audit:        auditRec,
		TargetPolicy: targetPol,
		OnActivated: func(evCtx context.Context, ev api.ActivationEvent) {
			_ = eventPub.PublishHint(evCtx, events.ConfigChangeHint{
				OrgID:       ev.OrgID,
				Version:     ev.Version,
				Checksum:    ev.Checksum,
				PublishedAt: ev.PublishedAt,
			})
		},
	}
	syncH := &api.SyncHandler{Store: st, SyncTokens: []string{syncToken}}

	cpSrv := httptest.NewServer(api.NewRouter(api.RouterOptions{
		Logger: silent,
		MountV1: func(v1 chi.Router) {
			authH.MountAuthRoutes(v1)
			svcH.MountServiceRoutes(v1)
			upH.MountUpstreamRoutes(v1)
			rtH.MountRouteRoutes(v1)
			consH.MountConsumerRoutes(v1)
			polH.MountPolicyRoutes(v1)
			pubH.MountPublicationRoutes(v1)
			syncH.MountGatewaySyncRoutes(v1)
		},
	}))
	defer cpSrv.Close()

	startReplica := func(name string, rlEnforcer *ratelimit.Enforcer) (*gateway.Server, string) {
		gw := gateway.NewServer(gateway.ServerConfig{
			Addr:               "127.0.0.1:0",
			AllowMockUpstreams: true,
			AllowLoopback:      true,
			ShutdownTimeout:    2 * time.Second,
		})
		gw.AttachRateLimiter(rlEnforcer)
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen %s: %v", name, err)
		}
		hints := events.NewHintCoalescer()
		go events.Subscribe(ctx, rc, channel, hints, silent)
		poller := syncer.NewPoller(gw.TableHolder(), syncer.PollerConfig{
			ControlPlaneURL: cpSrv.URL,
			SyncToken:       syncToken,
			Interval:        150 * time.Millisecond,
			Logger:          silent,
			Hints:           hints,
		})
		gw.AttachSyncStatus(poller.Status)
		go poller.Run(ctx)
		go func() { _ = gw.Serve(ctx, ln) }()
		return gw, "http://" + ln.Addr().String()
	}

	gw1, gw1URL := startReplica("gw-1", ratelimit.NewEnforcer(
		ratelimit.NewRedisLimiter(rc, 500*time.Millisecond), nil,
		ratelimit.NewRedisQuotaCounter(rc, 500*time.Millisecond), nil,
	))
	gw2, gw2URL := startReplica("gw-2", ratelimit.NewEnforcer(
		ratelimit.NewRedisLimiter(rc, 500*time.Millisecond), nil,
		ratelimit.NewRedisQuotaCounter(rc, 500*time.Millisecond), nil,
	))
	// Third replica with broken Redis client (nil -> returns error) to verify fail_open vs fail_closed!
	gwOutage, gwOutageURL := startReplica("gw-outage", ratelimit.NewEnforcer(
		ratelimit.NewRedisLimiter(nil, 100*time.Millisecond), nil,
		ratelimit.NewRedisQuotaCounter(nil, 100*time.Millisecond), nil,
	))

	client := &http.Client{Timeout: 5 * time.Second}
	postCP := func(path string, body any, cookie *http.Cookie, csrf string) (*http.Response, []byte) {
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, cpSrv.URL+path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			req.AddCookie(cookie)
		}
		if csrf != "" {
			req.Header.Set("X-CSRF-Token", csrf)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return resp, b
	}

	resp, b := postCP("/api/v1/auth/bootstrap", map[string]string{
		"email": "rl@e2e.test", "password": "VeryStrongPassword!99", "display_name": "RL Admin", "org_name": "RLOrg", "org_slug": "rl-org",
	}, nil, "")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("bootstrap=%d %s", resp.StatusCode, b)
	}
	var boot struct {
		OrgID string `json:"org_id"`
	}
	_ = json.Unmarshal(b, &boot)

	resp, b = postCP("/api/v1/auth/login", map[string]string{"email": "rl@e2e.test", "password": "VeryStrongPassword!99"}, nil, "")
	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == auth.SessionCookieName {
			cookie = c
		}
	}
	var loginRes struct {
		CSRFToken string `json:"csrf_token"`
	}
	_ = json.Unmarshal(b, &loginRes)

	_, b = postCP(fmt.Sprintf("/api/v1/orgs/%s/services", boot.OrgID), map[string]any{"slug": "orders", "name": "Orders"}, cookie, loginRes.CSRFToken)
	var svc struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(b, &svc)

	_, b = postCP(fmt.Sprintf("/api/v1/orgs/%s/upstreams", boot.OrgID), map[string]any{
		"name": "mock", "destination_class": "mock_local", "targets": []map[string]any{{"url": mockURL, "weight": 100}},
	}, cookie, loginRes.CSRFToken)
	var up struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(b, &up)

	makeRoute := func(name, prefix string) string {
		r, rb := postCP(fmt.Sprintf("/api/v1/orgs/%s/routes", boot.OrgID), map[string]any{
			"service_id": svc.ID, "upstream_pool_id": up.ID, "name": name,
			"host": "rl.gateway.test", "path_prefix": prefix, "methods": []string{"GET"},
			"strip_prefix": true, "priority": 100, "enabled": true,
		}, cookie, loginRes.CSRFToken)
		if r.StatusCode != http.StatusCreated {
			t.Fatalf("route %s: %d %s", name, r.StatusCode, rb)
		}
		var out struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(rb, &out)
		return out.ID
	}

	stdRouteID := makeRoute("standard-limited", "/std")
	critRouteID := makeRoute("critical-limited", "/crit")

	attachPol := func(name, ptype string, cfg any, routeID string) {
		r, rb := postCP(fmt.Sprintf("/api/v1/orgs/%s/policies", boot.OrgID), map[string]any{
			"name": name, "policy_type": ptype, "config": cfg,
		}, cookie, loginRes.CSRFToken)
		if r.StatusCode != http.StatusCreated {
			t.Fatalf("policy %s: %d %s", name, r.StatusCode, rb)
		}
		var p struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(rb, &p)
		r, rb = postCP(fmt.Sprintf("/api/v1/orgs/%s/policies/%s/attachments", boot.OrgID, p.ID), map[string]any{
			"scope_type": "route", "route_id": routeID, "priority": 100,
		}, cookie, loginRes.CSRFToken)
		if r.StatusCode != http.StatusCreated {
			t.Fatalf("attach %s: %d %s", name, r.StatusCode, rb)
		}
	}

	attachPol("std-rl", "rate_limit", map[string]any{
		"key_by": "ip", "requests_per_window": 12, "window_seconds": 60, "burst": 12, "fail_mode": "fail_open",
	}, stdRouteID)
	attachPol("crit-rl", "rate_limit", map[string]any{
		"key_by": "ip", "requests_per_window": 5, "window_seconds": 60, "burst": 5, "security_class": "critical", "fail_mode": "fail_closed",
	}, critRouteID)

	resp, b = postCP(fmt.Sprintf("/api/v1/orgs/%s/publications", boot.OrgID), map[string]string{"notes": "rl v1"}, cookie, loginRes.CSRFToken)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("publish: %d %s", resp.StatusCode, b)
	}

	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if gw1.TableHolder().Current().Version == 1 && gw2.TableHolder().Current().Version == 1 && gwOutage.TableHolder().Current().Version == 1 {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if gw1.TableHolder().Current().Version != 1 || gw2.TableHolder().Current().Version != 1 {
		t.Fatal("replicas failed to converge to v1")
	}

	// Send 40 concurrent requests split 20 -> gw1 and 20 -> gw2:
	// Combined across BOTH replicas, EXACTLY 12 must return 200 and 28 must return 429!
	var okCount, limitedCount, retryHeaderOK atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		targetBase := gw1URL
		if i%2 == 1 {
			targetBase = gw2URL
		}
		go func(base string) {
			defer wg.Done()
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/std/echo", nil)
			req.Host = "rl.gateway.test"
			r, err := client.Do(req)
			if err != nil {
				t.Errorf("request failed: %v", err)
				return
			}
			_, _ = io.Copy(io.Discard, r.Body)
			_ = r.Body.Close()
			switch r.StatusCode {
			case http.StatusOK:
				okCount.Add(1)
			case http.StatusTooManyRequests:
				limitedCount.Add(1)
				if r.Header.Get("Retry-After") != "" && r.Header.Get("RateLimit") != "" {
					retryHeaderOK.Add(1)
				}
			default:
				t.Errorf("unexpected status=%d", r.StatusCode)
			}
		}(targetBase)
	}
	wg.Wait()

	if okCount.Load() != 12 || limitedCount.Load() != 28 {
		t.Fatalf("two-replica global consistency failed: ok=%d limited=%d (want 12 ok, 28 limited)", okCount.Load(), limitedCount.Load())
	}
	if retryHeaderOK.Load() != 28 {
		t.Fatalf("expected all 28 429 responses to include Retry-After and RateLimit headers, got %d", retryHeaderOK.Load())
	}

	// Verify outage replica fail_open vs fail_closed behavior.
	reqOpen, _ := http.NewRequestWithContext(ctx, http.MethodGet, gwOutageURL+"/std/echo", nil)
	reqOpen.Host = "rl.gateway.test"
	rOpen, err := client.Do(reqOpen)
	if err != nil {
		t.Fatalf("outage fail_open request: %v", err)
	}
	_ = rOpen.Body.Close()
	if rOpen.StatusCode != http.StatusOK || rOpen.Header.Get("X-RateLimit-Mode") != "fail-open-degraded" {
		t.Fatalf("expected 200 + fail-open-degraded, got status=%d mode=%q", rOpen.StatusCode, rOpen.Header.Get("X-RateLimit-Mode"))
	}

	reqCrit, _ := http.NewRequestWithContext(ctx, http.MethodGet, gwOutageURL+"/crit/echo", nil)
	reqCrit.Host = "rl.gateway.test"
	rCrit, err := client.Do(reqCrit)
	if err != nil {
		t.Fatalf("outage fail_closed request: %v", err)
	}
	_ = rCrit.Body.Close()
	if rCrit.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("critical fail_closed route returned %d during Redis outage, want 503", rCrit.StatusCode)
	}
}
