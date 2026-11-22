package proxy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/YASSERRMD/GatewayForge/internal/gateway/table"
	"github.com/YASSERRMD/GatewayForge/internal/shared/policy"
	"github.com/YASSERRMD/GatewayForge/internal/shared/snapshot"
)

func TestPerRouteTimeoutPolicyAndClientCancellation(t *testing.T) {
	var upstreamCanceled atomic.Bool
	slowUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(400 * time.Millisecond):
			w.WriteHeader(http.StatusOK)
		case <-r.Context().Done():
			upstreamCanceled.Store(true)
		}
	}))
	defer slowUpstream.Close()

	timeoutCfg, _ := json.Marshal(policy.TimeoutPolicy{
		ConnectTimeoutMS: 50,
		TotalTimeoutMS:   120,
	})
	holder := table.NewHolder()
	_, err := holder.Apply(snapshot.Snapshot{
		SchemaVersion: snapshot.SchemaVersion,
		Version:       1,
		Pools: []snapshot.UpstreamPool{{
			ID: "p1", ConnectTimeoutMS: 2000, ReadTimeoutMS: 5000,
			Targets: []snapshot.UpstreamTarget{{ID: "t1", URL: slowUpstream.URL, Weight: 100, Enabled: true}},
		}},
		Routes: []snapshot.Route{{
			ID: "r1", Host: "api.local", PathPrefix: "/slow", Methods: []string{"GET"},
			UpstreamPoolID: "p1", Enabled: true,
			Policies: []snapshot.AttachedPolicy{{ID: "pol-t", PolicyType: "timeout", ScopeType: "route", Config: timeoutCfg}},
		}},
	}, false)
	if err != nil {
		t.Fatalf("apply snapshot: %v", err)
	}

	pol := DestinationPolicy{AllowLoopback: true}
	eng := NewEngine(holder, NewSafeTransport(DefaultTransportConfig(), pol), pol)

	// 1. Per-route total_timeout_ms (120ms) overrides slower 5000ms pool timeout -> 504 Gateway Timeout!
	req := httptest.NewRequest(http.MethodGet, "/slow", nil)
	req.Host = "api.local"
	rec := httptest.NewRecorder()
	start := time.Now()
	eng.ServeHTTP(rec, req)
	elapsed := time.Since(start)
	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status=%d want 504", rec.Code)
	}
	if elapsed > 350*time.Millisecond {
		t.Fatalf("per-route timeout took too long: %v", elapsed)
	}

	// 2. Client cancels after 40ms (before route 120ms timeout) -> 499 and upstream context canceled.
	upstreamCanceled.Store(false)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(40 * time.Millisecond)
		cancel()
	}()
	req2 := httptest.NewRequest(http.MethodGet, "/slow", nil).WithContext(ctx)
	req2.Host = "api.local"
	rec2 := httptest.NewRecorder()
	eng.ServeHTTP(rec2, req2)
	if rec2.Code != 499 {
		t.Fatalf("client cancel status=%d want 499", rec2.Code)
	}
	time.Sleep(30 * time.Millisecond)
	if !upstreamCanceled.Load() {
		t.Fatal("expected upstream request context to be canceled when client disconnects")
	}
}
