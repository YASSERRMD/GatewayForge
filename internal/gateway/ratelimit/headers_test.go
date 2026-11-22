package ratelimit

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestWriteHeadersAllowedAndDenied(t *testing.T) {
	t.Run("allowed response omits Retry-After and sets RateLimit headers", func(t *testing.T) {
		rec := httptest.NewRecorder()
		WriteHeaders(rec.Header(), Decision{
			Allowed:    true,
			Limit:      100,
			Remaining:  73,
			ResetAfter: 14500 * time.Millisecond, // ceil -> 15
			Window:     60 * time.Second,
		})
		if got := rec.Header().Get("RateLimit-Policy"); got != "100;w=60" {
			t.Fatalf("RateLimit-Policy=%q want 100;w=60", got)
		}
		if got := rec.Header().Get("RateLimit"); got != "limit=100, remaining=73, reset=15" {
			t.Fatalf("RateLimit=%q", got)
		}
		if got := rec.Header().Get("X-RateLimit-Remaining"); got != "73" {
			t.Fatalf("X-RateLimit-Remaining=%q", got)
		}
		if got := rec.Header().Get("Retry-After"); got != "" {
			t.Fatalf("unexpected Retry-After=%q on allowed response", got)
		}
	})

	t.Run("denied response sets Retry-After >= 1 and remaining=0", func(t *testing.T) {
		rec := httptest.NewRecorder()
		WriteHeaders(rec.Header(), Decision{
			Allowed:    false,
			Limit:      10,
			Remaining:  0,
			ResetAfter: 12100 * time.Millisecond, // ceil -> 13
			RetryAfter: 150 * time.Millisecond,   // ceil -> 1
			Window:     10 * time.Second,
		})
		if got := rec.Header().Get("Retry-After"); got != "1" {
			t.Fatalf("Retry-After=%q want 1", got)
		}
		if got := rec.Header().Get("RateLimit"); got != "limit=10, remaining=0, reset=13" {
			t.Fatalf("RateLimit=%q", got)
		}
		if got := rec.Header().Get("RateLimit-Policy"); got != "10;w=10" {
			t.Fatalf("RateLimit-Policy=%q", got)
		}
	})
}
