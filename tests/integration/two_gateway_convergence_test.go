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
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/YASSERRMD/GatewayForge/internal/control/api"
	"github.com/YASSERRMD/GatewayForge/internal/control/auth"
	"github.com/YASSERRMD/GatewayForge/internal/control/store"
	"github.com/YASSERRMD/GatewayForge/internal/gateway"
	syncer "github.com/YASSERRMD/GatewayForge/internal/gateway/sync"
	"github.com/YASSERRMD/GatewayForge/internal/mockupstream"
	"github.com/YASSERRMD/GatewayForge/internal/shared/cache"
	"github.com/YASSERRMD/GatewayForge/internal/shared/config"
	"github.com/YASSERRMD/GatewayForge/internal/shared/events"
	"github.com/YASSERRMD/GatewayForge/internal/shared/netsec"
)

func TestTwoGatewayReplicaZeroRestartConvergence(t *testing.T) {
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

	channel := "gatewayforge:test:two-gw-convergence"
	eventPub := events.NewPublisher(rc, channel)

	hasher := newTestArgonHasher()
	bootstrap := auth.NewBootstrapService(pool, config.EnvTest, hasher)
	sessions := auth.NewSessionManager(pool, hasher, time.Hour, false)
	st := store.New(pool)
	auditRec := api.NewAuditRecorder(st)
	targetPol := netsec.DefaultTargetPolicy(true)
	targetPol.AllowLoopback = true // host-side test targets Compose 127.0.0.1:18089

	const syncToken = "conv-sync-token-0123456789"
	authH := &api.AuthHandler{Bootstrap: bootstrap, Sessions: sessions}
	svcH := &api.ServiceHandler{Store: st, Sessions: sessions, Audit: auditRec}
	upH := &api.UpstreamHandler{Store: st, Sessions: sessions, Audit: auditRec, TargetPolicy: targetPol}
	rtH := &api.RouteHandler{Store: st, Sessions: sessions, Audit: auditRec}
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

	silent := slog.New(slog.NewJSONHandler(io.Discard, nil))
	cpRouter := api.NewRouter(api.RouterOptions{
		Logger: silent,
		MountV1: func(v1 chi.Router) {
			authH.MountAuthRoutes(v1)
			svcH.MountServiceRoutes(v1)
			upH.MountUpstreamRoutes(v1)
			rtH.MountRouteRoutes(v1)
			pubH.MountPublicationRoutes(v1)
			syncH.MountGatewaySyncRoutes(v1)
		},
	})
	cpSrv := httptest.NewServer(cpRouter)
	defer cpSrv.Close()

	// Start TWO independent gateway replicas subscribed to Redis hints + periodic polling.
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
			Interval:        500 * time.Millisecond,
			Logger:          silent,
			Hints:           hints,
		})
		gw.AttachSyncStatus(poller.Status)
		go poller.Run(ctx)
		go func() { _ = gw.Serve(ctx, ln) }()
		return gw, "http://" + ln.Addr().String()
	}

	gw1, gw1URL := startReplica("gateway-1")
	gw2, gw2URL := startReplica("gateway-2")
	time.Sleep(150 * time.Millisecond)

	// Bootstrap admin & login against live control plane HTTP server.
	httpClient := &http.Client{Timeout: 5 * time.Second}
	bootResp, err := httpClient.Post(cpSrv.URL+"/api/v1/auth/bootstrap", "application/json",
		bytes.NewBufferString(`{"email":"owner@conv.test","display_name":"Owner","password":"StrongPassword!123","org_slug":"conv-org","org_name":"Conv Org"}`))
	if err != nil || bootResp.StatusCode != http.StatusCreated {
		t.Fatalf("bootstrap failed: err=%v resp=%v", err, bootResp)
	}
	var boot auth.BootstrapResult
	_ = json.NewDecoder(bootResp.Body).Decode(&boot)
	_ = bootResp.Body.Close()

	loginResp, err := httpClient.Post(cpSrv.URL+"/api/v1/auth/login", "application/json",
		bytes.NewBufferString(`{"email":"owner@conv.test","password":"StrongPassword!123"}`))
	if err != nil || loginResp.StatusCode != http.StatusOK {
		t.Fatalf("login failed: err=%v", err)
	}
	cookie := loginResp.Cookies()[0]
	var loginOut struct {
		CSRFToken string `json:"csrf_token"`
	}
	_ = json.NewDecoder(loginResp.Body).Decode(&loginOut)
	_ = loginResp.Body.Close()

	authedPost := func(path, body string) map[string]any {
		req, _ := http.NewRequest(http.MethodPost, cpSrv.URL+path, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(auth.CSRFHeaderName, loginOut.CSRFToken)
		req.AddCookie(cookie)
		resp, err := httpClient.Do(req)
		if err != nil {
			t.Fatalf("POST %s failed: %v", path, err)
		}
		defer func() { _ = resp.Body.Close() }()
		raw, _ := io.ReadAll(resp.Body)
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			t.Fatalf("POST %s status=%d body=%s", path, resp.StatusCode, string(raw))
		}
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		return out
	}

	// 1. Create service, mock upstream pool, and route v1 (/echo) -> publish v1!
	svc := authedPost("/api/v1/orgs/"+boot.OrgID+"/services", `{"slug":"echo","name":"Echo Service"}`)
	up := authedPost("/api/v1/orgs/"+boot.OrgID+"/upstreams",
		fmt.Sprintf(`{"name":"mock-pool","destination_class":"mock_local","targets":[{"url":"%s","weight":100}]}`, mockURL))
	authedPost("/api/v1/orgs/"+boot.OrgID+"/routes",
		fmt.Sprintf(`{"service_id":"%s","upstream_pool_id":"%s","name":"echo-v1","host":"conv.local","path_prefix":"/echo","methods":["GET"],"priority":100}`, svc["id"], up["id"]))
	authedPost("/api/v1/orgs/"+boot.OrgID+"/publications", `{"notes":"release v1"}`)

	waitConverged := func(wantVersion int) string {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			t1, t2 := gw1.TableHolder().Current(), gw2.TableHolder().Current()
			if t1.Version == wantVersion && t2.Version == wantVersion && t1.Checksum != "" && t1.Checksum == t2.Checksum {
				return t1.Checksum
			}
			time.Sleep(25 * time.Millisecond)
		}
		t.Fatalf("replicas did not converge to version %d: gw1=(v%d %s) gw2=(v%d %s)",
			wantVersion, gw1.TableHolder().Current().Version, gw1.TableHolder().Current().Checksum,
			gw2.TableHolder().Current().Version, gw2.TableHolder().Current().Checksum)
		return ""
	}

	v1Checksum := waitConverged(1)
	t.Logf("Both replicas converged to v1 checksum=%s", v1Checksum)

	// Verify live proxied traffic on BOTH replicas!
	for i, base := range []string{gw1URL, gw2URL} {
		req, _ := http.NewRequest(http.MethodGet, base+"/echo/live?replica=1", nil)
		req.Host = "conv.local"
		resp, err := httpClient.Do(req)
		if err != nil {
			t.Fatalf("replica %d proxy error: %v", i+1, err)
		}
		var echo mockupstream.EchoResponse
		_ = json.NewDecoder(resp.Body).Decode(&echo)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK || echo.Path != "/echo/live" {
			t.Fatalf("replica %d unexpected response: status=%d echo=%+v", i+1, resp.StatusCode, echo)
		}
	}

	// 2. Add second route (/v2/status with strip_prefix=true) and publish v2 WITHOUT restarting gateways!
	authedPost("/api/v1/orgs/"+boot.OrgID+"/routes",
		fmt.Sprintf(`{"service_id":"%s","upstream_pool_id":"%s","name":"status-v2","host":"conv.local","path_prefix":"/v2","methods":["GET"],"strip_prefix":true,"priority":100}`, svc["id"], up["id"]))
	authedPost("/api/v1/orgs/"+boot.OrgID+"/publications", `{"notes":"release v2"}`)

	v2Checksum := waitConverged(2)
	if v2Checksum == v1Checksum {
		t.Fatal("expected distinct checksum for v2")
	}
	t.Logf("Both replicas converged to v2 checksum=%s without restart", v2Checksum)

	for i, base := range []string{gw1URL, gw2URL} {
		req, _ := http.NewRequest(http.MethodGet, base+"/v2/status/201", nil)
		req.Host = "conv.local"
		resp, err := httpClient.Do(req)
		if err != nil {
			t.Fatalf("replica %d v2 proxy error: %v", i+1, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("replica %d expected 201 on v2 route, got %d", i+1, resp.StatusCode)
		}
	}

	// 3. Roll back to v1 -> both replicas converge back to activation_revision=3 (v1 routes) and /v2/status/201 returns 404!
	authedPost("/api/v1/orgs/"+boot.OrgID+"/publications/1/activate", `{"reason":"rollback test"}`)
	waitConverged(3)
	for i, base := range []string{gw1URL, gw2URL} {
		req, _ := http.NewRequest(http.MethodGet, base+"/v2/status/201", nil)
		req.Host = "conv.local"
		resp, err := httpClient.Do(req)
		if err != nil {
			t.Fatalf("replica %d rollback check error: %v", i+1, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("replica %d expected 404 after rollback to v1, got %d", i+1, resp.StatusCode)
		}
	}
}
