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
	syncer "github.com/YASSERRMD/GatewayForge/internal/gateway/sync"
	"github.com/YASSERRMD/GatewayForge/internal/shared/cache"
	"github.com/YASSERRMD/GatewayForge/internal/shared/config"
	"github.com/YASSERRMD/GatewayForge/internal/shared/events"
	"github.com/YASSERRMD/GatewayForge/internal/shared/netsec"
)

func TestTwoReplicaFaultInjectionResilienceE2E(t *testing.T) {
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

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	rc, err := cache.Connect(ctx, redisAddr, redisPw)
	if err != nil {
		t.Fatalf("connect redis: %v", err)
	}
	defer func() { _ = rc.Close() }()

	// Programmable fault-injecting upstream.
	var cbFailMode atomic.Bool
	var cbUpstreamHits atomic.Int32
	var flakyGetCount atomic.Int32
	var flakyPostCount atomic.Int32
	var slowCanceled atomic.Bool
	var bulkheadHold = make(chan struct{})
	var bulkheadEntered = make(chan struct{}, 8)

	faultUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cb":
			cbUpstreamHits.Add(1)
			if cbFailMode.Load() {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		case "/retry":
			if r.Method == http.MethodGet {
				n := flakyGetCount.Add(1)
				if n%3 != 0 {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				w.WriteHeader(http.StatusOK)
				return
			}
			flakyPostCount.Add(1)
			w.WriteHeader(http.StatusServiceUnavailable)
		case "/slow":
			select {
			case <-time.After(450 * time.Millisecond):
				w.WriteHeader(http.StatusOK)
			case <-r.Context().Done():
				slowCanceled.Store(true)
			}
		case "/bh":
			bulkheadEntered <- struct{}{}
			select {
			case <-bulkheadHold:
				w.WriteHeader(http.StatusOK)
			case <-r.Context().Done():
			}
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer faultUpstream.Close()

	silent := slog.New(slog.NewJSONHandler(io.Discard, nil))
	channel := fmt.Sprintf("gatewayforge:test:fault-e2e:%d", time.Now().UnixNano())
	eventPub := events.NewPublisher(rc, channel)

	hasher := newTestArgonHasher()
	bootstrap := auth.NewBootstrapService(pool, config.EnvTest, hasher)
	sessions := auth.NewSessionManager(pool, hasher, time.Hour, false)
	st := store.New(pool)
	auditRec := api.NewAuditRecorder(st)
	targetPol := netsec.DefaultTargetPolicy(true)
	targetPol.AllowLoopback = true

	const syncToken = "fault-e2e-sync-token-123456"
	authH := &api.AuthHandler{Bootstrap: bootstrap, Sessions: sessions}
	svcH := &api.ServiceHandler{Store: st, Sessions: sessions, Audit: auditRec}
	upH := &api.UpstreamHandler{Store: st, Sessions: sessions, Audit: auditRec, TargetPolicy: targetPol}
	rtH := &api.RouteHandler{Store: st, Sessions: sessions, Audit: auditRec}
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
			polH.MountPolicyRoutes(v1)
			pubH.MountPublicationRoutes(v1)
			syncH.MountGatewaySyncRoutes(v1)
		},
	}))
	defer cpSrv.Close()

	startReplica := func(name string) (*gateway.Server, string) {
		gw := gateway.NewServer(gateway.ServerConfig{
			Addr:               "127.0.0.1:0",
			AllowMockUpstreams: true,
			AllowLoopback:      true,
			ShutdownTimeout:    2 * time.Second,
		})
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

	gw1, gw1URL := startReplica("gw-1")
	gw2, gw2URL := startReplica("gw-2")
	replicas := []string{gw1URL, gw2URL}

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
		"email": "res@e2e.test", "password": "VeryStrongPassword!99", "display_name": "Res Admin", "org_name": "ResOrg", "org_slug": "res-org",
	}, nil, "")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("bootstrap=%d %s", resp.StatusCode, b)
	}
	var boot struct {
		OrgID string `json:"org_id"`
	}
	_ = json.Unmarshal(b, &boot)

	resp, b = postCP("/api/v1/auth/login", map[string]string{"email": "res@e2e.test", "password": "VeryStrongPassword!99"}, nil, "")
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

	_, b = postCP(fmt.Sprintf("/api/v1/orgs/%s/services", boot.OrgID), map[string]any{"slug": "resilience", "name": "Resilience Svc"}, cookie, loginRes.CSRFToken)
	var svc struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(b, &svc)

	makePool := func(name string) string {
		r, rb := postCP(fmt.Sprintf("/api/v1/orgs/%s/upstreams", boot.OrgID), map[string]any{
			"name": name, "destination_class": "mock_local",
			"targets": []map[string]any{{"url": faultUpstream.URL, "weight": 100}},
		}, cookie, loginRes.CSRFToken)
		if r.StatusCode != http.StatusCreated {
			t.Fatalf("pool %s: %d %s", name, r.StatusCode, rb)
		}
		var p struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(rb, &p)
		return p.ID
	}

	cbPoolID := makePool("cb-pool")
	retryPoolID := makePool("retry-pool")
	slowPoolID := makePool("slow-pool")
	bhPoolID := makePool("bh-pool")

	makeRoute := func(name, prefix, poolID string, methods []string) string {
		r, rb := postCP(fmt.Sprintf("/api/v1/orgs/%s/routes", boot.OrgID), map[string]any{
			"service_id": svc.ID, "upstream_pool_id": poolID, "name": name,
			"host": "fault.gateway.test", "path_prefix": prefix, "methods": methods,
			"strip_prefix": false, "priority": 100, "enabled": true,
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

	cbRouteID := makeRoute("cb-route", "/cb", cbPoolID, []string{"GET"})
	retryRouteID := makeRoute("retry-route", "/retry", retryPoolID, []string{"GET", "POST"})
	slowRouteID := makeRoute("slow-route", "/slow", slowPoolID, []string{"GET"})
	bhRouteID := makeRoute("bh-route", "/bh", bhPoolID, []string{"GET"})

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

	attachPol("cb-pol", "circuit_breaker", map[string]any{
		"failure_threshold": 2, "success_threshold": 2, "open_duration_ms": 500, "half_open_max_in_flight": 1,
	}, cbRouteID)
	attachPol("retry-pol", "retry", map[string]any{
		"max_attempts": 3, "base_backoff_ms": 15, "max_backoff_ms": 50, "retryable_methods": []string{"GET"},
	}, retryRouteID)
	attachPol("slow-timeout", "timeout", map[string]any{
		"connect_timeout_ms": 100, "total_timeout_ms": 150,
	}, slowRouteID)
	attachPol("bh-limit", "timeout", map[string]any{
		"total_timeout_ms": 3000, "max_concurrent": 2,
	}, bhRouteID)

	resp, b = postCP(fmt.Sprintf("/api/v1/orgs/%s/publications", boot.OrgID), map[string]string{"notes": "fault v1"}, cookie, loginRes.CSRFToken)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("publish: %d %s", resp.StatusCode, b)
	}

	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if gw1.TableHolder().Current().Version == 1 && gw2.TableHolder().Current().Version == 1 {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if gw1.TableHolder().Current().Version != 1 || gw2.TableHolder().Current().Version != 1 {
		t.Fatal("replicas failed to converge to v1")
	}

	callGW := func(reqCtx context.Context, base, method, path string) (*http.Response, []byte) {
		req, _ := http.NewRequestWithContext(reqCtx, method, base+path, nil)
		req.Host = "fault.gateway.test"
		r, err := client.Do(req)
		if err != nil {
			return nil, nil
		}
		rb, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		return r, rb
	}

	// Scenario 1: Circuit breaker trips OPEN on upstream down -> fast-fails without hitting upstream -> HALF-OPEN recovers -> CLOSED!
	cbFailMode.Store(true)
	for idx, gwURL := range replicas {
		cbUpstreamHits.Store(0)
		// 2 failures trip the circuit breaker (failure_threshold=2).
		r1, _ := callGW(ctx, gwURL, http.MethodGet, "/cb")
		r2, _ := callGW(ctx, gwURL, http.MethodGet, "/cb")
		if r1.StatusCode != http.StatusServiceUnavailable || r2.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("gw%d expected 503 during upstream outage", idx+1)
		}
		hitsBeforeOpen := cbUpstreamHits.Load()
		// 3rd call must fail fast from OPEN circuit breaker WITHOUT hitting upstream!
		r3, rb3 := callGW(ctx, gwURL, http.MethodGet, "/cb")
		if r3.StatusCode != http.StatusServiceUnavailable || !bytes.Contains(rb3, []byte("circuit breaker is open")) {
			t.Fatalf("gw%d expected open circuit 503, got %d %s", idx+1, r3.StatusCode, rb3)
		}
		if cbUpstreamHits.Load() != hitsBeforeOpen {
			t.Fatalf("gw%d open circuit still dialed upstream!", idx+1)
		}
	}

	// Recover upstream and wait for open_duration_ms (500ms) -> half-open recovers on BOTH replicas!
	cbFailMode.Store(false)
	time.Sleep(550 * time.Millisecond)
	for idx, gwURL := range replicas {
		for attempt := 0; attempt < 2; attempt++ {
			r, _ := callGW(ctx, gwURL, http.MethodGet, "/cb")
			if r.StatusCode != http.StatusOK {
				t.Fatalf("gw%d half-open recovery attempt %d got %d want 200", idx+1, attempt+1, r.StatusCode)
			}
		}
	}

	// Scenario 2: Idempotent GET retries succeed transparently on BOTH replicas; POST NEVER retries!
	for idx, gwURL := range replicas {
		flakyGetCount.Store(0)
		rGet, _ := callGW(ctx, gwURL, http.MethodGet, "/retry")
		if rGet.StatusCode != http.StatusOK || flakyGetCount.Load() != 3 {
			t.Fatalf("gw%d GET retry failed: status=%d hits=%d", idx+1, rGet.StatusCode, flakyGetCount.Load())
		}
		flakyPostCount.Store(0)
		rPost, _ := callGW(ctx, gwURL, http.MethodPost, "/retry")
		if rPost.StatusCode != http.StatusServiceUnavailable || flakyPostCount.Load() != 1 {
			t.Fatalf("gw%d POST must not retry: status=%d hits=%d", idx+1, rPost.StatusCode, flakyPostCount.Load())
		}
	}

	// Scenario 3: Latency spike -> per-route timeout returns 504 on BOTH replicas; client cancel cancels upstream cleanly.
	for idx, gwURL := range replicas {
		rSlow, _ := callGW(ctx, gwURL, http.MethodGet, "/slow")
		if rSlow.StatusCode != http.StatusGatewayTimeout {
			t.Fatalf("gw%d slow route expected 504, got %d", idx+1, rSlow.StatusCode)
		}
	}
	slowCanceled.Store(false)
	cancelCtx, cancelReq := context.WithTimeout(ctx, 40*time.Millisecond)
	defer cancelReq()
	_, _ = callGW(cancelCtx, gw1URL, http.MethodGet, "/slow")
	time.Sleep(40 * time.Millisecond)
	if !slowCanceled.Load() {
		t.Fatal("expected client cancellation to propagate to upstream")
	}

	// Scenario 4: Bulkhead max_concurrent=2 rejects excess load with 503 + Retry-After, then recovers cleanly!
	var wg sync.WaitGroup
	var okBH, overloadedBH atomic.Int32
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, _ := callGW(ctx, gw1URL, http.MethodGet, "/bh")
			if r != nil && r.StatusCode == http.StatusOK {
				okBH.Add(1)
			}
		}()
	}
	<-bulkheadEntered
	<-bulkheadEntered

	// 3rd concurrent request while 2 are in-flight must immediately receive 503 service_overloaded!
	rOver, rbOver := callGW(ctx, gw1URL, http.MethodGet, "/bh")
	if rOver.StatusCode != http.StatusServiceUnavailable || rOver.Header.Get("Retry-After") != "1" || !bytes.Contains(rbOver, []byte("service_overloaded")) {
		t.Fatalf("expected 503 service_overloaded with Retry-After:1, got %d %s", rOver.StatusCode, rbOver)
	}
	overloadedBH.Add(1)

	close(bulkheadHold)
	wg.Wait()
	if okBH.Load() != 2 || overloadedBH.Load() != 1 {
		t.Fatalf("bulkhead counts unexpected: ok=%d overloaded=%d", okBH.Load(), overloadedBH.Load())
	}
}
