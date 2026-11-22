package proxy

import (
	"errors"
	"net/http"
	"net/netip"
	"net/url"
	"testing"
)

func TestValidateTargetURLAndSSRFControls(t *testing.T) {
	prodPol := DefaultDestinationPolicy(false)
	devPol := DefaultDestinationPolicy(true)

	t.Run("blocks metadata IPs and hosts even in dev mode", func(t *testing.T) {
		for _, bad := range []string{
			"http://169.254.169.254/latest/meta-data/",
			"http://metadata.google.internal/computeMetadata/v1/",
			"http://[fd00:ec2::254]/",
		} {
			if _, err := ValidateTargetURL(bad, devPol); !errors.Is(err, ErrBlockedDestination) {
				t.Fatalf("expected ErrBlockedDestination for %q, got %v", bad, err)
			}
		}
	})

	t.Run("blocks loopback and private IPs in production mode", func(t *testing.T) {
		for _, bad := range []string{
			"http://127.0.0.1:8080",
			"http://localhost:8080",
			"http://10.0.0.12:8080",
			"http://192.168.1.5:8080",
			"http://172.16.0.10:8080",
			"file:///etc/passwd",
			"http://user:pass@example.com",
		} {
			if _, err := ValidateTargetURL(bad, prodPol); !errors.Is(err, ErrBlockedDestination) {
				t.Fatalf("expected ErrBlockedDestination for %q in prod, got %v", bad, err)
			}
		}
	})

	t.Run("permits named mock-upstream on private Docker IP in dev mode only", func(t *testing.T) {
		dockerIP := netip.MustParseAddr("172.18.0.5")
		if err := ValidateDialAddr(dockerIP, "mock-upstream", devPol); err != nil {
			t.Fatalf("expected mock-upstream allowed in dev mode, got: %v", err)
		}
		if err := ValidateDialAddr(dockerIP, "unlisted-internal", devPol); !errors.Is(err, ErrBlockedDestination) {
			t.Fatalf("expected unlisted internal host blocked in dev mode, got: %v", err)
		}
		if err := ValidateDialAddr(dockerIP, "mock-upstream", prodPol); !errors.Is(err, ErrBlockedDestination) {
			t.Fatalf("expected mock-upstream blocked in prod mode, got: %v", err)
		}
	})
}

func TestBuildUpstreamURL(t *testing.T) {
	target, _ := url.Parse("http://mock-upstream:8089/base")

	t.Run("preserves prefix and raw query", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, "/api/orders?limit=10&tag=a%2Bb", nil)
		req.RequestURI = "/api/orders?limit=10&tag=a%2Bb"
		out, err := BuildUpstreamURL(target, req, "/api", false)
		if err != nil {
			t.Fatalf("BuildUpstreamURL: %v", err)
		}
		if out.String() != "http://mock-upstream:8089/base/api/orders?limit=10&tag=a%2Bb" {
			t.Fatalf("unexpected upstream URL: %s", out.String())
		}
	})

	t.Run("strips configured prefix cleanly", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, "/api/orders/99?full=1", nil)
		req.RequestURI = "/api/orders/99?full=1"
		out, err := BuildUpstreamURL(target, req, "/api", true)
		if err != nil {
			t.Fatalf("BuildUpstreamURL strip: %v", err)
		}
		if out.String() != "http://mock-upstream:8089/base/orders/99?full=1" {
			t.Fatalf("unexpected stripped URL: %s", out.String())
		}
	})

	t.Run("rejects absolute open-proxy RequestURI", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, "http://evil.example/steal", nil)
		req.RequestURI = "http://evil.example/steal"
		if _, err := BuildUpstreamURL(target, req, "/", false); !errors.Is(err, ErrInvalidProxyRequest) {
			t.Fatalf("expected ErrInvalidProxyRequest for absolute URI, got: %v", err)
		}
	})

	t.Run("rejects raw and encoded path traversal", func(t *testing.T) {
		for _, p := range []string{"/api/../admin", "/api/%2e%2e/admin", "/api/..\\admin"} {
			req := &http.Request{
				Method:     http.MethodGet,
				RequestURI: p,
				URL:        &url.URL{Path: p, RawPath: p},
			}
			if _, err := BuildUpstreamURL(target, req, "/api", false); !errors.Is(err, ErrInvalidProxyRequest) {
				t.Fatalf("expected traversal rejection for %q, got %v", p, err)
			}
		}
	})
}
