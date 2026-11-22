package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/YASSERRMD/GatewayForge/internal/gateway"
	"github.com/YASSERRMD/GatewayForge/internal/gateway/proxy"
	"github.com/YASSERRMD/GatewayForge/internal/mockupstream"
	"github.com/YASSERRMD/GatewayForge/internal/shared/snapshot"
)

func TestGatewayNoConfigAndLiveComposeProxySmoke(t *testing.T) {
	if os.Getenv("INTEGRATION_TESTS") != "1" {
		t.Skip("set INTEGRATION_TESTS=1 to run gateway proxy Compose smoke test")
	}
	mockURL := os.Getenv("MOCK_UPSTREAM_URL")
	if mockURL == "" {
		mockURL = "http://127.0.0.1:18089"
	}

	gwSrv := gateway.NewServer(gateway.ServerConfig{
		Addr:               "127.0.0.1:0",
		AllowMockUpstreams: true,
		AllowLoopback:      true, // host-side test connects to 127.0.0.1:18089 Compose port
		ShutdownTimeout:    2 * time.Second,
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen gateway: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = gwSrv.Serve(ctx, ln) }()

	gwBase := "http://" + ln.Addr().String()
	client := &http.Client{Timeout: 5 * time.Second}

	// 1. No-config default-deny: unknown route MUST return 404 route_not_found with X-Request-Id.
	reqNoCfg, _ := http.NewRequest(http.MethodGet, gwBase+"/echo/test", nil)
	reqNoCfg.Host = "api.local"
	respNoCfg, err := client.Do(reqNoCfg)
	if err != nil {
		t.Fatalf("no-config request failed: %v", err)
	}
	var ge proxy.GatewayError
	_ = json.NewDecoder(respNoCfg.Body).Decode(&ge)
	_ = respNoCfg.Body.Close()
	if respNoCfg.StatusCode != http.StatusNotFound || ge.Code != proxy.CodeRouteNotFound || ge.RequestID == "" {
		t.Fatalf("expected 404 route_not_found with request_id, got status=%d body=%+v", respNoCfg.StatusCode, ge)
	}

	// 2. Apply checksummed snapshot pointing to live Compose mock-upstream.
	snap := snapshot.Snapshot{
		SchemaVersion: snapshot.SchemaVersion,
		OrgID:         "org-smoke",
		Version:       1,
		PublishedAt:   time.Now().UTC(),
		Pools: []snapshot.UpstreamPool{
			{
				ID:               "pool-mock",
				Name:             "mock-echo",
				DestinationClass: "mock_local",
				ConnectTimeoutMS: 2000,
				ReadTimeoutMS:    5000,
				Targets: []snapshot.UpstreamTarget{
					{ID: "t-mock-1", URL: mockURL, Weight: 100, Enabled: true},
				},
			},
		},
		Routes: []snapshot.Route{
			{
				ID:             "route-echo",
				OrgID:          "org-smoke",
				UpstreamPoolID: "pool-mock",
				Name:           "echo",
				Host:           "api.local",
				PathPrefix:     "/echo",
				Methods:        []string{"GET", "POST"},
				StripPrefix:    false,
				Priority:       100,
				Enabled:        true,
			},
			{
				ID:             "route-strip-status",
				OrgID:          "org-smoke",
				UpstreamPoolID: "pool-mock",
				Name:           "strip-status",
				Host:           "api.local",
				PathPrefix:     "/v1/mock",
				Methods:        []string{"GET"},
				StripPrefix:    true,
				Priority:       100,
				Enabled:        true,
			},
		},
	}
	sum, err := snap.ComputeChecksum()
	if err != nil {
		t.Fatalf("compute checksum: %v", err)
	}
	snap.Checksum = sum
	if _, err := gwSrv.TableHolder().Apply(snap, true); err != nil {
		t.Fatalf("apply snapshot: %v", err)
	}

	// 3. Configured POST /echo/orders?env=smoke reaches live Compose mock-upstream!
	reqEcho, _ := http.NewRequest(http.MethodPost, gwBase+"/echo/orders?env=smoke", bytes.NewBufferString(`{"item":"widget"}`))
	reqEcho.Host = "api.local"
	reqEcho.Header.Set("X-Request-Id", "smoke-phase04-001")
	reqEcho.Header.Set("X-Forwarded-For", "1.2.3.4") // spoofed from untrusted peer
	reqEcho.Header.Set("Connection", "X-Smuggled-Hop")
	reqEcho.Header.Set("X-Smuggled-Hop", "drop-me")
	respEcho, err := client.Do(reqEcho)
	if err != nil {
		t.Fatalf("proxy echo request failed: %v", err)
	}
	var echoOut mockupstream.EchoResponse
	_ = json.NewDecoder(respEcho.Body).Decode(&echoOut)
	_ = respEcho.Body.Close()

	if respEcho.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from mock-upstream, got %d", respEcho.StatusCode)
	}
	if echoOut.Method != http.MethodPost || echoOut.Path != "/echo/orders" || echoOut.RawQuery != "env=smoke" || echoOut.Body != `{"item":"widget"}` {
		t.Fatalf("unexpected echoed payload: %+v", echoOut)
	}
	if got := echoOut.Headers["X-Request-Id"]; len(got) != 1 || got[0] != "smoke-phase04-001" {
		t.Fatalf("missing propagated X-Request-Id: %+v", echoOut.Headers)
	}
	if got := echoOut.Headers["X-Forwarded-For"]; len(got) != 1 || got[0] != "127.0.0.1" {
		t.Fatalf("spoofed X-Forwarded-For was not sanitized: %+v", got)
	}
	if _, leaked := echoOut.Headers["X-Smuggled-Hop"]; leaked {
		t.Fatalf("hop-by-hop header X-Smuggled-Hop leaked to upstream!")
	}

	// 4. Strip-prefix route forwards /v1/mock/status/201 -> /status/201.
	reqStrip, _ := http.NewRequest(http.MethodGet, gwBase+"/v1/mock/status/201", nil)
	reqStrip.Host = "api.local"
	respStrip, err := client.Do(reqStrip)
	if err != nil {
		t.Fatalf("strip-prefix request failed: %v", err)
	}
	_ = respStrip.Body.Close()
	if respStrip.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 from stripped /status/201 route, got %d", respStrip.StatusCode)
	}

	// 5. Open-proxy check: unknown host MUST return 404 route_not_found.
	reqOpen, _ := http.NewRequest(http.MethodGet, gwBase+"/echo/orders", nil)
	reqOpen.Host = "evil.example"
	respOpen, err := client.Do(reqOpen)
	if err != nil {
		t.Fatalf("open-proxy check failed: %v", err)
	}
	_ = respOpen.Body.Close()
	if respOpen.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 rejecting unknown host (no open proxy), got %d", respOpen.StatusCode)
	}
}
