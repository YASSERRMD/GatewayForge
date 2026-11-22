package security

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/YASSERRMD/GatewayForge/internal/gateway/proxy"
	"github.com/YASSERRMD/GatewayForge/internal/shared/netsec"
)

func TestSSRFBlockedDestinationURLsAcrossModes(t *testing.T) {
	prodPol := netsec.DefaultTargetPolicy(false)
	devPol := netsec.DefaultTargetPolicy(true)

	alwaysBlocked := []string{
		"http://169.254.169.254/latest/meta-data/",
		"http://169.254.170.2/v2/credentials",
		"http://[fd00:ec2::254]/latest/meta-data",
		"http://metadata.google.internal/computeMetadata/v1/",
		"http://metadata.goog/",
		"http://[::ffff:169.254.169.254]/latest",
		"http://[::ffff:127.0.0.1]:8080/admin",
		"http://[::ffff:10.0.0.5]/internal",
		"http://127.0.0.1:5432/",
		"http://[::1]:6379/",
		"http://localhost:8080/admin",
		"http://api.localhost/secret",
		"http://0.0.0.0:8080/",
		"http://10.0.0.1/internal",
		"http://172.16.5.4/internal",
		"http://192.168.1.10/router",
		"file:///etc/passwd",
		"gopher://127.0.0.1:6379/_INFO",
		"ftp://example.com/file",
		"http://admin:secret@example.com/api",
		"https://example.com/api#fragment",
	}

	for _, raw := range alwaysBlocked {
		if _, err := netsec.ValidateTargetURL(raw, prodPol); err == nil {
			t.Fatalf("prod policy MUST reject %q", raw)
		}
		if _, err := netsec.ValidateTargetURL(raw, devPol); err == nil {
			t.Fatalf("dev policy MUST reject %q", raw)
		}
	}

	// Mock upstream allowed ONLY when AllowMockUpstreams=true
	if _, err := netsec.ValidateTargetURL("http://mock-upstream:8089", prodPol); err == nil {
		t.Fatal("prod policy MUST reject mock-upstream when AllowMockUpstreams=false")
	}
	if _, err := netsec.ValidateTargetURL("http://mock-upstream:8089", devPol); err != nil {
		t.Fatalf("dev policy should allow mock-upstream, got %v", err)
	}
}

func TestConnectTimeDNSRebindingAndIPv6MappedBlocked(t *testing.T) {
	pol := proxy.DefaultDestinationPolicy(false)

	rebindIPs := []net.IP{
		net.ParseIP("169.254.169.254"),
		net.ParseIP("169.254.170.2"),
		net.ParseIP("127.0.0.1"),
		net.ParseIP("::1"),
		net.ParseIP("10.0.0.42"),
		net.ParseIP("172.20.1.5"),
		net.ParseIP("192.168.0.10"),
		net.ParseIP("::ffff:169.254.169.254"),
		net.ParseIP("::ffff:127.0.0.1"),
		net.ParseIP("0.0.0.0"),
	}

	for _, ip := range rebindIPs {
		// Attacker uses a benign-looking public hostname that DNS-rebinds to a restricted IP at dial time
		err := proxy.ValidateDialIP(ip, "rebind-attacker.example.com", pol)
		if !errors.Is(err, proxy.ErrBlockedDestination) {
			t.Fatalf("expected ErrBlockedDestination when hostname rebinds to %s, got %v", ip.String(), err)
		}
	}

	// Verify NewSafeTransport blocks literal metadata dial before establishing TCP connection
	tr := proxy.NewSafeTransport(proxy.DefaultTransportConfig(), pol)
	_, err := tr.DialContext(context.Background(), "tcp", "169.254.169.254:80")
	if !errors.Is(err, proxy.ErrBlockedDestination) {
		t.Fatalf("expected SafeTransport DialContext to block metadata IP, got %v", err)
	}
	_, err = tr.DialContext(context.Background(), "tcp", "[::ffff:127.0.0.1]:80")
	if !errors.Is(err, proxy.ErrBlockedDestination) {
		t.Fatalf("expected SafeTransport DialContext to block IPv6-mapped loopback, got %v", err)
	}
}

func TestSafeTransportDoesNotFollowRedirectsToMetadata(t *testing.T) {
	metadataTouched := false
	fakeMeta := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		metadataTouched = true
		w.WriteHeader(http.StatusOK)
	}))
	defer fakeMeta.Close()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, fakeMeta.URL+"/latest/meta-data/", http.StatusFound)
	}))
	defer upstream.Close()

	// Gateway proxy uses Transport.RoundTrip directly (never http.Client automatic redirect chasing).
	pol := proxy.DestinationPolicy{AllowLoopback: true}
	tr := proxy.NewSafeTransport(proxy.DefaultTransportConfig(), pol)
	req, _ := http.NewRequest(http.MethodGet, upstream.URL+"/trigger-redirect", nil)
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("roundtrip failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 302 returned verbatim without following redirect, got %d", resp.StatusCode)
	}
	if metadataTouched {
		t.Fatal("proxy transport followed redirect to metadata target")
	}
}
