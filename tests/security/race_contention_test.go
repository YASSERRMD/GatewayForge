package security

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gwauth "github.com/YASSERRMD/GatewayForge/internal/gateway/auth"
	"github.com/YASSERRMD/GatewayForge/internal/gateway/proxy"
	"github.com/YASSERRMD/GatewayForge/internal/gateway/ratelimit"
	"github.com/YASSERRMD/GatewayForge/internal/gateway/table"
	"github.com/YASSERRMD/GatewayForge/internal/shared/apikey"
	"github.com/YASSERRMD/GatewayForge/internal/shared/policy"
	"github.com/YASSERRMD/GatewayForge/internal/shared/snapshot"
)

func TestConcurrentConfigSwapKeyRevocationAndProxyTrafficRaceFree(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	pepper := []byte("race-test-pepper-32-bytes-minimum!")
	keyA, _ := apikey.Generate(pepper)

	buildSnap := func(ver int, includeKey bool) snapshot.Snapshot {
		consumers := []snapshot.Consumer{}
		if includeKey {
			consumers = append(consumers, snapshot.Consumer{
				ID:     "c1",
				Name:   "race-consumer",
				Status: "active",
				Keys:   []snapshot.APIKey{{ID: "k1", KeyPrefix: keyA.Prefix, KeyHash: keyA.Hash, Scopes: []string{"read"}}},
			})
		}
		authCfg, _ := json.Marshal(gwauth.APIKeyPolicyConfig{HeaderName: "X-Api-Key"})
		return snapshot.Snapshot{
			SchemaVersion: snapshot.SchemaVersion,
			OrgID:         "org-race",
			Version:       ver,
			Pools: []snapshot.UpstreamPool{
				{ID: "p1", Name: "race-pool", Targets: []snapshot.UpstreamTarget{{ID: "t1", URL: upstream.URL, Weight: 100, Enabled: true}}},
			},
			Routes: []snapshot.Route{
				{
					ID: "r1", OrgID: "org-race", UpstreamPoolID: "p1", Name: "race-route",
					Host: "race.local", PathPrefix: "/v1/items", Methods: []string{"GET"}, Priority: 100, Enabled: true,
					Policies: []snapshot.AttachedPolicy{
						{ID: "pol1", Name: "auth", PolicyType: "api_key", SchemaVersion: 1, ScopeType: "route", Priority: 100, Config: authCfg},
					},
				},
			},
			Consumers: consumers,
		}
	}

	holder := table.NewHolder()
	if _, err := holder.Apply(buildSnap(1, true), false); err != nil {
		t.Fatalf("initial apply: %v", err)
	}

	pol := proxy.DefaultDestinationPolicy(true)
	pol.AllowLoopback = true
	engine := proxy.NewEngine(holder, nil, pol)
	engine.Auth = &gwauth.Enforcer{APIKeys: gwauth.NewAPIKeyAuthenticator(pepper)}

	var wg sync.WaitGroup
	var okCount, unauthCount, unexpectedCount atomic.Int32
	stop := make(chan struct{})

	// Writer goroutine rapidly swapping config versions and toggling key revocation
	wg.Add(1)
	go func() {
		defer wg.Done()
		for v := 2; v <= 30; v++ {
			_, _ = holder.Apply(buildSnap(v, v%2 == 0), false)
			time.Sleep(1 * time.Millisecond)
		}
		close(stop)
	}()

	// 12 concurrent proxy clients with occasional request cancellations
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for n := 0; ; n++ {
				select {
				case <-stop:
					return
				default:
				}
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				if n%7 == 0 {
					cancel()
				}
				req := httptest.NewRequest(http.MethodGet, "/v1/items", nil).WithContext(ctx)
				req.Host = "race.local"
				req.Header.Set("X-Api-Key", keyA.Plaintext)
				rec := httptest.NewRecorder()
				engine.ServeHTTP(rec, req)
				cancel()

				switch rec.Code {
				case http.StatusOK:
					okCount.Add(1)
				case http.StatusUnauthorized, http.StatusBadGateway, http.StatusGatewayTimeout, 499:
					unauthCount.Add(1)
				default:
					unexpectedCount.Add(1)
				}
			}
		}(i)
	}
	wg.Wait()

	if unexpectedCount.Load() != 0 {
		t.Fatalf("encountered %d unexpected status codes during concurrent swap/revocation", unexpectedCount.Load())
	}
	if okCount.Load() == 0 || unauthCount.Load() == 0 {
		t.Fatalf("expected both authorized (200) and revoked (401) observations during swap, got ok=%d unauth=%d", okCount.Load(), unauthCount.Load())
	}
}

func TestRateLimiterHighContentionAtomicCeiling(t *testing.T) {
	limiter := ratelimit.NewLocalLimiter(nil, 1000)
	rlPol := policy.RateLimitPolicy{
		Algorithm:         policy.AlgoTokenBucket,
		KeyBy:             policy.KeyByConsumer,
		RequestsPerWindow: 25,
		WindowSeconds:     60,
		Burst:             25,
		SecurityClass:     policy.ClassStandard,
		FailMode:          policy.FailOpen,
	}
	rlPol.Normalize()

	const workers = 32
	const perWorker = 10
	var allowed, denied atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < perWorker; i++ {
				dec, err := limiter.Allow(context.Background(), "org-race:consumer-1", rlPol)
				if err != nil {
					t.Errorf("unexpected limiter err: %v", err)
					return
				}
				if dec.Allowed {
					allowed.Add(1)
				} else {
					denied.Add(1)
				}
			}
		}()
	}
	close(start)
	wg.Wait()

	if allowed.Load() != 25 || denied.Load() != workers*perWorker-25 {
		t.Fatalf("expected exact burst ceiling 25 allowed and %d denied, got allowed=%d denied=%d",
			workers*perWorker-25, allowed.Load(), denied.Load())
	}
}
