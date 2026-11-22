package ratelimit_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/YASSERRMD/GatewayForge/internal/gateway/proxy"
	"github.com/YASSERRMD/GatewayForge/internal/gateway/ratelimit"
	"github.com/YASSERRMD/GatewayForge/internal/gateway/table"
	"github.com/YASSERRMD/GatewayForge/internal/shared/policy"
	"github.com/YASSERRMD/GatewayForge/internal/shared/snapshot"
)

func TestConfigChangeInvalidationPreventsStalePrivilegeEscalation(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	makeSnap := func(version int, burst int64) snapshot.Snapshot {
		rawPol, _ := json.Marshal(policy.RateLimitPolicy{
			KeyBy:             policy.KeyByIP,
			RequestsPerWindow: burst,
			WindowSeconds:     60,
			Burst:             burst,
		})
		return snapshot.Snapshot{
			SchemaVersion: snapshot.SchemaVersion,
			OrgID:         "org-1",
			Version:       version,
			Pools: []snapshot.UpstreamPool{{
				ID:               "p1",
				ConnectTimeoutMS: 2000,
				ReadTimeoutMS:    5000,
				Targets:          []snapshot.UpstreamTarget{{ID: "t1", URL: upstream.URL, Weight: 100, Enabled: true}},
			}},
			Routes: []snapshot.Route{{
				ID:             "r1",
				Host:           "api.example.com",
				PathPrefix:     "/items",
				Methods:        []string{"GET"},
				UpstreamPoolID: "p1",
				Enabled:        true,
				Policies: []snapshot.AttachedPolicy{{
					ID:         "pol-rl-1",
					PolicyType: "rate_limit",
					ScopeType:  "route",
					Config:     rawPol,
				}},
			}},
		}
	}

	holder := table.NewHolder()
	if _, err := holder.Apply(makeSnap(1, 100), false); err != nil {
		t.Fatalf("apply v1: %v", err)
	}

	pol := proxy.DestinationPolicy{AllowLoopback: true}
	eng := proxy.NewEngine(holder, proxy.NewSafeTransport(proxy.DefaultTransportConfig(), pol), pol)
	eng.RateLimit = ratelimit.NewEnforcer(ratelimit.NewLocalLimiter(nil, 1000), nil, nil, nil)

	// Consume 10 of 100 tokens under v1 (leaving 90 stale tokens in v1 bucket).
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest(http.MethodGet, "/items", nil)
		req.Host = "api.example.com"
		req.RemoteAddr = "198.51.100.55:12345"
		rec := httptest.NewRecorder()
		eng.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("v1 req %d status=%d want 200", i+1, rec.Code)
		}
	}

	// Tighten rule to burst=2 via v2 snapshot while load continues!
	// Because the key namespace includes ConfigDigest(), the 90 remaining tokens from v1 MUST NOT carry over.
	if _, err := holder.Apply(makeSnap(2, 2), false); err != nil {
		t.Fatalf("apply v2: %v", err)
	}

	var allowed, limited atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/items", nil)
			req.Host = "api.example.com"
			req.RemoteAddr = "198.51.100.55:12345"
			rec := httptest.NewRecorder()
			eng.ServeHTTP(rec, req)
			switch rec.Code {
			case http.StatusOK:
				allowed.Add(1)
			case http.StatusTooManyRequests:
				limited.Add(1)
			default:
				t.Errorf("unexpected status=%d", rec.Code)
			}
		}()
	}
	wg.Wait()

	if allowed.Load() != 2 || limited.Load() != 18 {
		t.Fatalf("after tightening to burst=2: allowed=%d limited=%d (want 2 allowed, 18 limited)", allowed.Load(), limited.Load())
	}
}
