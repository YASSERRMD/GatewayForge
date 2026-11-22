package proxy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/YASSERRMD/GatewayForge/internal/gateway/table"
	"github.com/YASSERRMD/GatewayForge/internal/shared/snapshot"
)

func TestRequestResourceLimitsAndCancellation(t *testing.T) {
	var upstreamCanceled atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/slow":
			select {
			case <-r.Context().Done():
				upstreamCanceled.Store(true)
			case <-time.After(2 * time.Second):
				w.WriteHeader(http.StatusOK)
			}
		case "/upload":
			_, err := io.ReadAll(r.Body)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer upstream.Close()

	holder := table.NewHolder()
	_, err := holder.Apply(snapshot.Snapshot{
		SchemaVersion: snapshot.SchemaVersion,
		OrgID:         "org-lim",
		Version:       1,
		Pools: []snapshot.UpstreamPool{
			{ID: "p1", ReadTimeoutMS: 2000, Targets: []snapshot.UpstreamTarget{{ID: "t1", URL: upstream.URL, Weight: 100, Enabled: true}}},
		},
		Routes: []snapshot.Route{
			{ID: "r-slow", UpstreamPoolID: "p1", Host: "lim.local", PathPrefix: "/slow", Methods: []string{"GET"}, Priority: 100, Enabled: true, RequestTimeoutMS: 80},
			{ID: "r-upload", UpstreamPoolID: "p1", Host: "lim.local", PathPrefix: "/upload", Methods: []string{"POST"}, Priority: 100, Enabled: true, MaxBodyBytes: 128},
		},
	}, false)
	if err != nil {
		t.Fatalf("apply snapshot: %v", err)
	}

	pol := DefaultDestinationPolicy(true)
	pol.AllowLoopback = true
	eng := NewEngine(holder, nil, pol)
	eng.Limits.MaxHeaderCount = 15
	gw := httptest.NewServer(eng)
	defer gw.Close()

	t.Run("rejects body exceeding route MaxBodyBytes with 413", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPost, gw.URL+"/upload", bytes.NewBufferString(strings.Repeat("x", 512)))
		req.Host = "lim.local"
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("do request: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusRequestEntityTooLarge {
			t.Fatalf("expected 413, got %d", resp.StatusCode)
		}
	})

	t.Run("rejects excessive header count with 431", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPost, gw.URL+"/upload", bytes.NewBufferString("ok"))
		req.Host = "lim.local"
		for i := 0; i < 20; i++ {
			req.Header.Set(fmt.Sprintf("X-Extra-%d", i), "v")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("do request: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusRequestHeaderFieldsTooLarge {
			t.Fatalf("expected 431, got %d", resp.StatusCode)
		}
	})

	t.Run("slow upstream triggers 504 deadline timeout and cancels upstream context", func(t *testing.T) {
		upstreamCanceled.Store(false)
		req, _ := http.NewRequest(http.MethodGet, gw.URL+"/slow", nil)
		req.Host = "lim.local"
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("do request: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusGatewayTimeout {
			t.Fatalf("expected 504 on slow upstream, got %d", resp.StatusCode)
		}
		// Give upstream handler a moment to observe canceled context.
		deadline := time.Now().Add(300 * time.Millisecond)
		for !upstreamCanceled.Load() && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if !upstreamCanceled.Load() {
			t.Fatal("expected upstream request context to be canceled on gateway timeout")
		}
	})

	t.Run("client disconnect cancels upstream cleanly", func(t *testing.T) {
		upstreamCanceled.Store(false)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, gw.URL+"/slow", nil)
		req.Host = "lim.local"
		_, _ = http.DefaultClient.Do(req)
		deadline := time.Now().Add(300 * time.Millisecond)
		for !upstreamCanceled.Load() && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if !upstreamCanceled.Load() {
			t.Fatal("expected upstream request context to be canceled when client disconnected")
		}
	})
}
