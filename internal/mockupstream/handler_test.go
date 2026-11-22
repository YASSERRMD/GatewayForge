package mockupstream

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMockUpstreamEndpoints(t *testing.T) {
	h := NewHandler("mock-test-1")

	t.Run("health", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
	})

	t.Run("echo preserves method, path, query, headers and body", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/echo/orders?limit=10&sort=asc", strings.NewReader(`{"ok":true}`))
		req.Header.Set("X-Test-Header", "hello")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		var out EchoResponse
		if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if out.Method != http.MethodPost || out.Path != "/echo/orders" || out.RawQuery != "limit=10&sort=asc" {
			t.Fatalf("unexpected echo metadata: %+v", out)
		}
		if out.Body != `{"ok":true}` || out.BodySize != 11 {
			t.Fatalf("unexpected echo body: %+v", out)
		}
		if got := out.Headers["X-Test-Header"]; len(got) != 1 || got[0] != "hello" {
			t.Fatalf("missing echoed header: %+v", out.Headers)
		}
	})

	t.Run("status returns requested HTTP code and rejects out of range", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/status/503", nil))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("expected 503, got %d", rec.Code)
		}

		bad := httptest.NewRecorder()
		h.ServeHTTP(bad, httptest.NewRequest(http.MethodGet, "/status/999", nil))
		if bad.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", bad.Code)
		}
	})

	t.Run("delay honors duration and context cancellation", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/delay/20", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
		defer cancel()
		recCancel := httptest.NewRecorder()
		h.ServeHTTP(recCancel, httptest.NewRequest(http.MethodGet, "/delay/5000", nil).WithContext(ctx))
		if recCancel.Code != 499 {
			t.Fatalf("expected 499 on cancellation, got %d", recCancel.Code)
		}
	})

	t.Run("headers applies query-requested response headers", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/headers?set=X-Custom:abc", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		if got := rec.Header().Get("X-Custom"); got != "abc" {
			t.Fatalf("expected X-Custom=abc, got %q", got)
		}
	})

	t.Run("stream emits exact requested byte count", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/stream/10000", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		if rec.Body.Len() != 10000 {
			t.Fatalf("expected 10000 bytes, got %d", rec.Body.Len())
		}
	})
}
