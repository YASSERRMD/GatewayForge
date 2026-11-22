package resilience_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/YASSERRMD/GatewayForge/internal/gateway/proxy"
	"github.com/YASSERRMD/GatewayForge/internal/gateway/resilience"
	"github.com/YASSERRMD/GatewayForge/internal/gateway/table"
	"github.com/YASSERRMD/GatewayForge/internal/shared/snapshot"
)

func TestTwoTargetFailoverAllDownAndProbeRecovery(t *testing.T) {
	var hits1, hits2 atomic.Int32
	up1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits1.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer up1.Close()

	up2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits2.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer up2.Close()

	var fail1, fail2 atomic.Bool
	ht := resilience.NewHealthTracker(resilience.ActiveProbeConfig{
		Interval:           time.Second,
		Timeout:            time.Second,
		HealthyThreshold:   2,
		UnhealthyThreshold: 2,
	}, nil, func(_ context.Context, targetURL, _ string) error {
		if targetURL == up1.URL && fail1.Load() {
			return errors.New("up1 down")
		}
		if targetURL == up2.URL && fail2.Load() {
			return errors.New("up2 down")
		}
		return nil
	})

	holder := table.NewHolder()
	tbl, err := holder.Apply(snapshot.Snapshot{
		SchemaVersion: snapshot.SchemaVersion,
		Version:       1,
		Pools: []snapshot.UpstreamPool{{
			ID: "p1", ConnectTimeoutMS: 2000, ReadTimeoutMS: 5000,
			Targets: []snapshot.UpstreamTarget{
				{ID: "t1", URL: up1.URL, Weight: 100, Enabled: true},
				{ID: "t2", URL: up2.URL, Weight: 100, Enabled: true},
			},
		}},
		Routes: []snapshot.Route{{
			ID: "r1", Host: "api.local", PathPrefix: "/data", Methods: []string{"GET"},
			UpstreamPoolID: "p1", Priority: 100, Enabled: true,
		}},
	}, false)
	if err != nil {
		t.Fatalf("apply snapshot: %v", err)
	}
	ht.SyncPools(tbl.Pools)

	pol := proxy.DestinationPolicy{AllowLoopback: true}
	eng := proxy.NewEngine(holder, proxy.NewSafeTransport(proxy.DefaultTransportConfig(), pol), pol)
	eng.Health = ht

	call := func() int {
		req := httptest.NewRequest(http.MethodGet, "/data", nil)
		req.Host = "api.local"
		rec := httptest.NewRecorder()
		eng.ServeHTTP(rec, req)
		return rec.Code
	}

	// 1. Mark t1 unhealthy via active probes -> 100% of traffic fails over to t2.
	fail1.Store(true)
	ht.ProbeAll(context.Background())
	ht.ProbeAll(context.Background())

	hits1.Store(0)
	hits2.Store(0)
	for i := 0; i < 6; i++ {
		if code := call(); code != http.StatusOK {
			t.Fatalf("expected 200 via t2 failover, got %d", code)
		}
	}
	if hits1.Load() != 0 || hits2.Load() != 6 {
		t.Fatalf("expected 0 hits on unhealthy t1 and 6 on t2, got t1=%d t2=%d", hits1.Load(), hits2.Load())
	}

	// 2. Mark t2 unhealthy as well -> all-down returns 503 Service Unavailable cleanly!
	fail2.Store(true)
	ht.ProbeAll(context.Background())
	ht.ProbeAll(context.Background())
	if code := call(); code != http.StatusServiceUnavailable {
		t.Fatalf("all-down expected 503, got %d", code)
	}

	// 3. Recover t1 via 2 healthy probes -> traffic immediately resumes on t1.
	fail1.Store(false)
	ht.ProbeAll(context.Background())
	ht.ProbeAll(context.Background())
	if code := call(); code != http.StatusOK {
		t.Fatalf("recovered t1 expected 200, got %d", code)
	}
	if hits1.Load() != 1 {
		t.Fatalf("expected recovered t1 to receive traffic, got hits1=%d", hits1.Load())
	}
}
