package control

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/YASSERRMD/GatewayForge/internal/shared/health"
)

func TestControlPlaneHealthEndpoints(t *testing.T) {
	srv := NewServer(ServerConfig{Addr: "127.0.0.1:0"})

	t.Run("live returns 200 ok", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/health/live", nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		var body health.Response
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if body.Status != "ok" || body.Service != "control-plane" {
			t.Fatalf("unexpected health body: %+v", body)
		}
	})

	t.Run("live rejects POST with 405", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/health/live", nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("expected 405, got %d", rec.Code)
		}
	})

	t.Run("ready returns 200 when checks pass and 503 when check fails", func(t *testing.T) {
		srv.Health().Register("postgres", func(ctx context.Context) error { return nil })

		req := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}

		srv.Health().Register("postgres", func(ctx context.Context) error { return errors.New("connection refused") })
		rec2 := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
		if rec2.Code != http.StatusServiceUnavailable {
			t.Fatalf("expected 503, got %d", rec2.Code)
		}
		var body health.Response
		if err := json.NewDecoder(rec2.Body).Decode(&body); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if body.Status != "not_ready" || body.Checks["postgres"].Status != "fail" {
			t.Fatalf("unexpected degraded readiness body: %+v", body)
		}
	})
}

func TestControlPlaneGracefulShutdown(t *testing.T) {
	srv := NewServer(ServerConfig{
		Addr:            "127.0.0.1:0",
		ShutdownTimeout: 2 * time.Second,
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- srv.Serve(ctx, ln)
	}()

	resp, err := http.Get("http://" + ln.Addr().String() + "/health/live")
	if err != nil {
		t.Fatalf("GET /health/live failed: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("expected clean shutdown, got: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for graceful shutdown")
	}
}
