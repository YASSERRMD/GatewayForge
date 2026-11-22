package security

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/YASSERRMD/GatewayForge/internal/gateway/proxy"
	"github.com/YASSERRMD/GatewayForge/internal/gateway/table"
	"github.com/YASSERRMD/GatewayForge/internal/shared/snapshot"
)

func TestProxyProtocolAbuseSmugglingSpoofingAndLimits(t *testing.T) {
	var upstreamHits atomic.Int32
	var seenHeaders http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits.Add(1)
		seenHeaders = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	holder := table.NewHolder()
	snap := snapshot.Snapshot{
		SchemaVersion: snapshot.SchemaVersion,
		OrgID:         "org-sec",
		Version:       1,
		Pools: []snapshot.UpstreamPool{
			{
				ID:      "p1",
				Name:    "safe-pool",
				Targets: []snapshot.UpstreamTarget{{ID: "t1", URL: upstream.URL, Weight: 100, Enabled: true}},
			},
		},
		Routes: []snapshot.Route{
			{
				ID:             "r1",
				OrgID:          "org-sec",
				UpstreamPoolID: "p1",
				Name:           "orders",
				Host:           "api.sec.local",
				PathPrefix:     "/v1/orders",
				Methods:        []string{"GET", "POST"},
				Priority:       100,
				Enabled:        true,
				MaxBodyBytes:   512,
			},
		},
	}
	if _, err := holder.Apply(snap, false); err != nil {
		t.Fatalf("apply snapshot: %v", err)
	}

	pol := proxy.DefaultDestinationPolicy(true)
	pol.AllowLoopback = true
	engine := proxy.NewEngine(holder, nil, pol)

	// 1. Open-proxy absolute URI rejected 400 without upstream hit
	openProxyReq := httptest.NewRequest(http.MethodGet, "/v1/orders", nil)
	openProxyReq.RequestURI = "http://evil.example/steal"
	openProxyReq.Host = "api.sec.local"
	rec1 := httptest.NewRecorder()
	engine.ServeHTTP(rec1, openProxyReq)
	if rec1.Code != http.StatusBadRequest {
		t.Fatalf("open-proxy request expected 400, got %d", rec1.Code)
	}

	// 2. Unmatched route returns 404 + X-Request-Id without touching upstream
	unmatchedReq := httptest.NewRequest(http.MethodGet, "/unknown/path", nil)
	unmatchedReq.Host = "other.local"
	rec2 := httptest.NewRecorder()
	engine.ServeHTTP(rec2, unmatchedReq)
	if rec2.Code != http.StatusNotFound || rec2.Header().Get("X-Request-Id") == "" {
		t.Fatalf("unmatched route expected 404 with X-Request-Id, got %d hdr=%q", rec2.Code, rec2.Header().Get("X-Request-Id"))
	}

	// 3. Encoded & raw path traversal rejected 400 without touching upstream
	for _, badPath := range []string{"/v1/orders/../../etc/passwd", "/v1/orders/%2e%2e/admin", "/v1/orders/%2E%2E/secret"} {
		req := httptest.NewRequest(http.MethodGet, badPath, nil)
		req.Host = "api.sec.local"
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("path traversal %q expected 400, got %d", badPath, rec.Code)
		}
	}

	// 4. Oversized body (> 512 bytes) rejected 413 without touching upstream
	bigReq := httptest.NewRequest(http.MethodPost, "/v1/orders", bytes.NewReader([]byte(strings.Repeat("X", 2048))))
	bigReq.Host = "api.sec.local"
	bigReq.ContentLength = 2048
	rec4 := httptest.NewRecorder()
	engine.ServeHTTP(rec4, bigReq)
	if rec4.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body expected 413, got %d", rec4.Code)
	}

	if upstreamHits.Load() != 0 {
		t.Fatalf("expected 0 upstream hits for all rejected abuse requests, got %d", upstreamHits.Load())
	}

	// 5. Hop-by-hop smuggling & spoofed X-Forwarded-* stripped from untrusted peer
	smugReq := httptest.NewRequest(http.MethodGet, "/v1/orders", nil)
	smugReq.Host = "api.sec.local"
	smugReq.RemoteAddr = "198.51.100.77:54321"
	smugReq.Header.Set("Connection", "keep-alive, X-Smuggled-Evil")
	smugReq.Header.Set("X-Smuggled-Evil", "pwned")
	smugReq.Header.Set("Proxy-Authorization", "Basic secret")
	smugReq.Header.Set("X-Forwarded-For", "127.0.0.1, 10.0.0.1")
	smugReq.Header.Set("X-Forwarded-Host", "spoofed.evil.com")
	smugReq.Header.Set("X-Forwarded-Proto", "https")

	rec5 := httptest.NewRecorder()
	engine.ServeHTTP(rec5, smugReq)
	if rec5.Code != http.StatusOK {
		t.Fatalf("valid request with stripped headers expected 200, got %d: %s", rec5.Code, rec5.Body.String())
	}
	if seenHeaders.Get("X-Smuggled-Evil") != "" {
		t.Fatalf("Connection-nominated hop-by-hop header leaked to upstream: %q", seenHeaders.Get("X-Smuggled-Evil"))
	}
	if seenHeaders.Get("Proxy-Authorization") != "" {
		t.Fatal("Proxy-Authorization hop-by-hop header leaked to upstream")
	}
	if gotXFF := seenHeaders.Get("X-Forwarded-For"); gotXFF != "198.51.100.77" {
		t.Fatalf("spoofed X-Forwarded-For was not replaced with peer IP; got %q", gotXFF)
	}
	if gotXFH := seenHeaders.Get("X-Forwarded-Host"); gotXFH != "api.sec.local" {
		t.Fatalf("spoofed X-Forwarded-Host was not replaced; got %q", gotXFH)
	}
}
