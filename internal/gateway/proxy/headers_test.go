package proxy

import (
	"net/http"
	"testing"
)

func TestSanitizeOutboundHeadersDefense(t *testing.T) {
	t.Run("strips hop-by-hop and Connection-nominated headers case-insensitively", func(t *testing.T) {
		in, _ := http.NewRequest(http.MethodGet, "/test", nil)
		in.RemoteAddr = "198.51.100.20:43210"
		in.Host = "api.local"
		out := in.Clone(in.Context())
		out.Header.Set("Connection", "keep-alive, x-custom-hop, X-Internal-Auth")
		out.Header.Set("Keep-Alive", "timeout=5")
		out.Header.Set("Proxy-Authorization", "Basic dXNlcjpwYXNz")
		out.Header.Set("X-Custom-Hop", "smuggled")
		out.Header.Set("x-internal-auth", "forged")
		out.Header.Set("X-Safe-Header", "preserved")

		SanitizeOutboundHeaders(in, out, TrustedProxyConfig{})

		for _, removed := range []string{"Connection", "Keep-Alive", "Proxy-Authorization", "X-Custom-Hop", "X-Internal-Auth"} {
			if got := out.Header.Get(removed); got != "" {
				t.Fatalf("expected header %q stripped, got %q", removed, got)
			}
		}
		if got := out.Header.Get("X-Safe-Header"); got != "preserved" {
			t.Fatalf("expected X-Safe-Header preserved, got %q", got)
		}
	})

	t.Run("untrusted peer cannot spoof X-Forwarded-* or Forwarded", func(t *testing.T) {
		in, _ := http.NewRequest(http.MethodGet, "/test", nil)
		in.RemoteAddr = "203.0.113.9:55555"
		in.Host = "api.local"
		in.Header.Set("X-Forwarded-For", "127.0.0.1")
		in.Header.Set("X-Forwarded-Proto", "https")
		in.Header.Set("X-Forwarded-Host", "admin.internal")
		in.Header.Set("X-Real-Ip", "10.0.0.1")
		in.Header.Set("Forwarded", "for=127.0.0.1;proto=https")
		out := in.Clone(in.Context())

		SanitizeOutboundHeaders(in, out, TrustedProxyConfig{})

		if got := out.Header.Get("X-Forwarded-For"); got != "203.0.113.9" {
			t.Fatalf("expected authoritative X-Forwarded-For=203.0.113.9, got %q", got)
		}
		if got := out.Header.Get("X-Forwarded-Proto"); got != "http" {
			t.Fatalf("expected authoritative X-Forwarded-Proto=http, got %q", got)
		}
		if got := out.Header.Get("X-Forwarded-Host"); got != "api.local" {
			t.Fatalf("expected authoritative X-Forwarded-Host=api.local, got %q", got)
		}
		if got := out.Header.Get("Forwarded"); got != "" {
			t.Fatalf("expected spoofed Forwarded stripped, got %q", got)
		}
		if got := out.Header.Get("X-Real-Ip"); got != "" {
			t.Fatalf("expected spoofed X-Real-Ip stripped, got %q", got)
		}
	})

	t.Run("trusted proxy peer preserves forwarded chain and appends peer", func(t *testing.T) {
		trusted, err := ParseTrustedProxies([]string{"10.10.0.0/16"})
		if err != nil {
			t.Fatalf("ParseTrustedProxies: %v", err)
		}
		in, _ := http.NewRequest(http.MethodGet, "/test", nil)
		in.RemoteAddr = "10.10.1.5:12345"
		in.Host = "api.local"
		in.Header.Set("X-Forwarded-For", "198.51.100.77")
		in.Header.Set("X-Forwarded-Proto", "https")
		out := in.Clone(in.Context())

		SanitizeOutboundHeaders(in, out, trusted)

		if got := out.Header.Get("X-Forwarded-For"); got != "198.51.100.77, 10.10.1.5" {
			t.Fatalf("unexpected X-Forwarded-For chain: %q", got)
		}
		if got := out.Header.Get("X-Forwarded-Proto"); got != "https" {
			t.Fatalf("expected preserved https proto, got %q", got)
		}
	})
}
