package gateway

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

func TestGatewayHealthAndDefaultDeny(t *testing.T) {
	srv := NewServer(ServerConfig{Addr: "127.0.0.1:0"})

	t.Run("live returns 200 ok for gateway", func(t *testing.T) {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health/live", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		var body health.Response
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body.Status != "ok" || body.Service != "gateway" {
			t.Fatalf("unexpected payload: %+v", body)
		}
	})

	t.Run("ready transitions to 503 when snapshot probe fails", func(t *testing.T) {
		srv.Health().Register("snapshot", func(ctx context.Context) error {
			return errors.New("no active snapshot")
		})
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("expected 503, got %d", rec.Code)
		}
	})

	t.Run("unknown route returns strict 404 JSON", func(t *testing.T) {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/anything", nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", rec.Code)
		}
		var body map[string]any
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body["code"] != "route_not_found" {
			t.Fatalf("expected route_not_found, got %+v", body)
		}
	})
}

func TestGatewayListenerBindSmoke(t *testing.T) {
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

	resp, err := http.Get("http://" + ln.Addr().String() + "/health/ready")
	if err != nil {
		t.Fatalf("GET /health/ready failed: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("shutdown returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for gateway shutdown")
	}
}
