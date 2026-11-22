package load

import (
	"context"
	"io"
	"net/http"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func runMultiTargetLoadProfile(t *testing.T, mode string, baseURLs []string, host, path string, workers int, dur time.Duration) loadSummary {
	t.Helper()
	tr := &http.Transport{
		MaxIdleConns:        workers * 2,
		MaxIdleConnsPerHost: workers * 2,
		IdleConnTimeout:     30 * time.Second,
	}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 5 * time.Second}

	var c200, c429, c503, cErr atomic.Int64
	var rr atomic.Uint64
	var latMu sync.Mutex
	latencies := make([]time.Duration, 0, 16384)

	ctx, cancel := context.WithTimeout(context.Background(), dur)
	defer cancel()

	start := time.Now()
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			localLat := make([]time.Duration, 0, 1024)
			for ctx.Err() == nil {
				idx := rr.Add(1) - 1
				target := baseURLs[int(idx)%len(baseURLs)]
				reqStart := time.Now()
				req, _ := http.NewRequestWithContext(ctx, http.MethodGet, target+path, nil)
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
		return latencies[int(float64(len(latencies)-1)*p)]
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

// TestBaselineGatewayLoadProfile executes repeated live baseline load trials against the seeded
// Compose gateway replica on 127.0.0.1:18081 (/echo, Host: localhost).
func TestBaselineGatewayLoadProfile(t *testing.T) {
	if os.Getenv("LOAD_TESTS") != "1" {
		t.Skip("set LOAD_TESTS=1 to execute live baseline load profile against Compose gateway")
	}

	gatewayURL := os.Getenv("GATEWAY_URL")
	if gatewayURL == "" {
		gatewayURL = "http://127.0.0.1:18081"
	}

	// Warm-up check ensures seeded /echo route is live before timing.
	req, _ := http.NewRequest(http.MethodGet, gatewayURL+"/echo", nil)
	req.Host = "localhost"
	resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("gateway warm-up request failed: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("gateway warm-up /echo returned %d; run 'make seed' and wait for gateway sync", resp.StatusCode)
	}

	const (
		trials   = 3
		workers  = 32
		duration = 5 * time.Second
	)
	for trial := 1; trial <= trials; trial++ {
		s := runMultiTargetLoadProfile(t, "gateway_baseline_echo", []string{gatewayURL}, "localhost", "/echo", workers, duration)
		t.Logf("BASELINE_TRIAL=%d workers=%d duration=%v total=%d rps=%.1f 200=%d err=%d p50=%v p95=%v p99=%v",
			trial, workers, s.Duration.Round(time.Millisecond), s.Total, s.RPS, s.Status200, s.StatusErr, s.P50, s.P95, s.P99)
		if s.Status200 == 0 || s.StatusErr > 0 {
			t.Fatalf("baseline trial %d failed: 200=%d err=%d", trial, s.Status200, s.StatusErr)
		}
	}
}

// TestResilienceComparisonProfiles runs repeated comparative trials across:
// 1. Direct mock-upstream (127.0.0.1:18089)
// 2. Single gateway replica (127.0.0.1:18081)
// 3. Dual gateway replicas round-robin (127.0.0.1:18081 + 127.0.0.1:18082)
func TestResilienceComparisonProfiles(t *testing.T) {
	if os.Getenv("LOAD_TESTS") != "1" {
		t.Skip("set LOAD_TESTS=1 to execute comparative load profiles against Compose stack")
	}

	mockURL := os.Getenv("MOCK_UPSTREAM_URL")
	if mockURL == "" {
		mockURL = "http://127.0.0.1:18089"
	}
	gw1URL := os.Getenv("GATEWAY_URL")
	if gw1URL == "" {
		gw1URL = "http://127.0.0.1:18081"
	}
	gw2URL := os.Getenv("GATEWAY_2_URL")
	if gw2URL == "" {
		gw2URL = "http://127.0.0.1:18082"
	}

	const (
		trials   = 3
		workers  = 32
		duration = 4 * time.Second
	)

	scenarios := []struct {
		name string
		urls []string
	}{
		{name: "direct_mock_upstream", urls: []string{mockURL}},
		{name: "single_gateway_replica", urls: []string{gw1URL}},
		{name: "dual_gateway_replicas", urls: []string{gw1URL, gw2URL}},
	}

	for _, sc := range scenarios {
		for trial := 1; trial <= trials; trial++ {
			s := runMultiTargetLoadProfile(t, sc.name, sc.urls, "localhost", "/echo", workers, duration)
			t.Logf("COMPARE scenario=%s trial=%d workers=%d duration=%v total=%d rps=%.1f 200=%d err=%d p50=%v p95=%v p99=%v",
				sc.name, trial, workers, s.Duration.Round(time.Millisecond), s.Total, s.RPS, s.Status200, s.StatusErr, s.P50, s.P95, s.P99)
			if s.Status200 == 0 || s.StatusErr > 0 {
				t.Fatalf("comparison %s trial %d had errors: 200=%d err=%d", sc.name, trial, s.Status200, s.StatusErr)
			}
		}
	}
}
