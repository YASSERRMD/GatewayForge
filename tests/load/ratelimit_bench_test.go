package load

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/YASSERRMD/GatewayForge/internal/gateway"
	"github.com/YASSERRMD/GatewayForge/internal/gateway/ratelimit"
	"github.com/YASSERRMD/GatewayForge/internal/shared/cache"
	"github.com/YASSERRMD/GatewayForge/internal/shared/policy"
	"github.com/YASSERRMD/GatewayForge/internal/shared/snapshot"
)

type loadSummary struct {
	Mode      string
	Duration  time.Duration
	Total     int64
	RPS       float64
	Status200 int64
	Status429 int64
	Status503 int64
	StatusErr int64
	P50       time.Duration
	P95       time.Duration
	P99       time.Duration
}

func runLoadProfile(t *testing.T, mode, url, host, path string, workers int, dur time.Duration) loadSummary {
	t.Helper()
	tr := &http.Transport{
		MaxIdleConns:        workers * 2,
		MaxIdleConnsPerHost: workers * 2,
		IdleConnTimeout:     30 * time.Second,
	}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 3 * time.Second}

	var c200, c429, c503, cErr atomic.Int64
	var latMu sync.Mutex
	latencies := make([]time.Duration, 0, 8192)

	ctx, cancel := context.WithTimeout(context.Background(), dur)
	defer cancel()

	start := time.Now()
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			localLat := make([]time.Duration, 0, 512)
			for ctx.Err() == nil {
				reqStart := time.Now()
				req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url+path, nil)
				req.Host = host
				resp, err := client.Do(req)
				if err != nil {
					if ctx.Err() == nil {
						cErr.Add(1)
					}
					continue
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
				localLat = append(localLat, time.Since(reqStart))
				switch resp.StatusCode {
				case http.StatusOK:
					c200.Add(1)
				case http.StatusTooManyRequests:
					c429.Add(1)
				case http.StatusServiceUnavailable:
					c503.Add(1)
				default:
					cErr.Add(1)
				}
			}
			latMu.Lock()
			latencies = append(latencies, localLat...)
			latMu.Unlock()
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	pct := func(p float64) time.Duration {
		if len(latencies) == 0 {
			return 0
		}
		idx := int(float64(len(latencies)-1) * p)
		return latencies[idx]
	}

	total := c200.Load() + c429.Load() + c503.Load() + cErr.Load()
	return loadSummary{
		Mode:      mode,
		Duration:  elapsed,
		Total:     total,
		RPS:       float64(total) / elapsed.Seconds(),
		Status200: c200.Load(),
		Status429: c429.Load(),
		Status503: c503.Load(),
		StatusErr: cErr.Load(),
		P50:       pct(0.50),
		P95:       pct(0.95),
		P99:       pct(0.99),
	}
}

func TestRateLimitThroughputBenchmark(t *testing.T) {
	if os.Getenv("LOAD_TESTS") != "1" {
		t.Skip("set LOAD_TESTS=1 to run live rate-limit throughput and latency benchmark")
	}

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

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	rc, err := cache.Connect(ctx, redisAddr, redisPw)
	if err != nil {
		t.Fatalf("connect redis: %v", err)
	}
	defer func() { _ = rc.Close() }()

	openPol, _ := json.Marshal(policy.RateLimitPolicy{
		KeyBy: policy.KeyByIP, RequestsPerWindow: 200, WindowSeconds: 10, Burst: 200, FailMode: policy.FailOpen,
	})
	closedPol, _ := json.Marshal(policy.RateLimitPolicy{
		KeyBy: policy.KeyByIP, RequestsPerWindow: 200, WindowSeconds: 10, Burst: 200, SecurityClass: policy.ClassCritical, FailMode: policy.FailClosed,
	})

	snap := snapshot.Snapshot{
		SchemaVersion: snapshot.SchemaVersion,
		OrgID:         "bench-org",
		Version:       1,
		Pools: []snapshot.UpstreamPool{{
			ID: "p1", ConnectTimeoutMS: 2000, ReadTimeoutMS: 5000,
			Targets: []snapshot.UpstreamTarget{{ID: "t1", URL: mockURL, Weight: 100, Enabled: true}},
		}},
		Routes: []snapshot.Route{
			{
				ID: "rt-open", Host: "bench.gateway.test", PathPrefix: "/open", Methods: []string{"GET"},
				StripPrefix: true, UpstreamPoolID: "p1", Priority: 100, Enabled: true,
				Policies: []snapshot.AttachedPolicy{{ID: "pol-open", PolicyType: "rate_limit", ScopeType: "route", Config: openPol}},
			},
			{
				ID: "rt-closed", Host: "bench.gateway.test", PathPrefix: "/closed", Methods: []string{"GET"},
				StripPrefix: true, UpstreamPoolID: "p1", Priority: 100, Enabled: true,
				Policies: []snapshot.AttachedPolicy{{ID: "pol-closed", PolicyType: "rate_limit", ScopeType: "route", Config: closedPol}},
			},
		},
	}

	startGW := func(enforcer *ratelimit.Enforcer) string {
		gw := gateway.NewServer(gateway.ServerConfig{
			Addr: "127.0.0.1:0", AllowMockUpstreams: true, AllowLoopback: true, ShutdownTimeout: 2 * time.Second,
		})
		if _, err := gw.TableHolder().Apply(snap, false); err != nil {
			t.Fatalf("apply snapshot: %v", err)
		}
		gw.AttachRateLimiter(enforcer)
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		go func() { _ = gw.Serve(ctx, ln) }()
		return "http://" + ln.Addr().String()
	}

	gwRedisURL := startGW(ratelimit.NewEnforcer(ratelimit.NewRedisLimiter(rc, 250*time.Millisecond), nil, nil, nil))
	gwOutageURL := startGW(ratelimit.NewEnforcer(ratelimit.NewRedisLimiter(nil, 250*time.Millisecond), nil, nil, nil))

	s1 := runLoadProfile(t, "redis_available_distributed", gwRedisURL, "bench.gateway.test", "/open/echo", 16, 3*time.Second)
	s2 := runLoadProfile(t, "redis_unavailable_fail_open", gwOutageURL, "bench.gateway.test", "/open/echo", 16, 3*time.Second)
	s3 := runLoadProfile(t, "redis_unavailable_fail_closed", gwOutageURL, "bench.gateway.test", "/closed/echo", 16, 3*time.Second)

	for _, s := range []loadSummary{s1, s2, s3} {
		t.Logf("BENCH mode=%s total=%d rps=%.1f 200=%d 429=%d 503=%d err=%d p50=%v p95=%v p99=%v",
			s.Mode, s.Total, s.RPS, s.Status200, s.Status429, s.Status503, s.StatusErr, s.P50, s.P95, s.P99)
	}
	if s1.Status200 == 0 || s1.Status429 == 0 {
		t.Fatalf("expected both allowed and rate-limited traffic when Redis is up: %+v", s1)
	}
	if s2.Status200 == 0 || s2.Status503 != 0 {
		t.Fatalf("expected fail_open to serve 200s during Redis outage: %+v", s2)
	}
	if s3.Status503 == 0 || s3.Status200 != 0 {
		t.Fatalf("expected fail_closed to block all requests with 503 during Redis outage: %+v", s3)
	}
}
