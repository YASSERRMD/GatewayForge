package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/YASSERRMD/GatewayForge/internal/shared/policy"
)

func TestDeriveRateLimitKeyIsolationAndNoRawSecrets(t *testing.T) {
	secretVal := "gwf_deadbeef1234.supersecretapikeypayload000000000000000000000000"
	req := httptest.NewRequest(http.MethodGet, "http://api.local/orders", nil)
	req.RemoteAddr = "203.0.113.42:54321"
	req.Header.Set("X-Tenant-Hint", secretVal)

	pol := policy.RateLimitPolicy{
		KeyBy:             policy.KeyByHeader,
		HeaderName:        "X-Tenant-Hint",
		RequestsPerWindow: 60,
		WindowSeconds:     60,
		Burst:             60,
	}

	kOrgA := DeriveRateLimitKey(KeyInput{
		OrgID: "org-alpha", PolicyID: "pol-1", ScopeType: "route", RouteID: "rt-1", Request: req,
	}, pol)
	kOrgB := DeriveRateLimitKey(KeyInput{
		OrgID: "org-beta", PolicyID: "pol-1", ScopeType: "route", RouteID: "rt-1", Request: req,
	}, pol)

	if kOrgA == kOrgB {
		t.Fatalf("cross-tenant key collision detected: %s", kOrgA)
	}
	if strings.Contains(kOrgA, secretVal) || strings.Contains(kOrgA, "supersecret") || strings.Contains(kOrgA, "203.0.113.42") {
		t.Fatalf("derived key leaked raw secret or IP: %s", kOrgA)
	}

	// Delimiter injection attempt across org/policy boundaries must never collide.
	inj1 := DeriveRateLimitKey(KeyInput{OrgID: "a:b", PolicyID: "c", RouteID: "r1", ConsumerID: "u1"}, policy.RateLimitPolicy{KeyBy: policy.KeyByConsumer, RequestsPerWindow: 10, WindowSeconds: 60})
	inj2 := DeriveRateLimitKey(KeyInput{OrgID: "a", PolicyID: "b:c", RouteID: "r1", ConsumerID: "u1"}, policy.RateLimitPolicy{KeyBy: policy.KeyByConsumer, RequestsPerWindow: 10, WindowSeconds: 60})
	if inj1 == inj2 {
		t.Fatalf("delimiter injection caused key collision: %s", inj1)
	}

	// Anonymous IP fallback must not collide with an authenticated consumer whose ID happens to equal the IP string.
	consPol := policy.RateLimitPolicy{KeyBy: policy.KeyByConsumer, RequestsPerWindow: 10, WindowSeconds: 60}
	anonKey := DeriveRateLimitKey(KeyInput{OrgID: "org-1", PolicyID: "p1", RouteID: "r1", ClientIP: "198.51.100.7:1234"}, consPol)
	authKey := DeriveRateLimitKey(KeyInput{OrgID: "org-1", PolicyID: "p1", RouteID: "r1", ConsumerID: "198.51.100.7"}, consPol)
	if anonKey == authKey {
		t.Fatalf("anonymous IP key collided with authenticated consumer key: %s", anonKey)
	}

	// Same IPv4 from different ephemeral ports must map to the exact same key.
	ipPol := policy.RateLimitPolicy{KeyBy: policy.KeyByIP, RequestsPerWindow: 10, WindowSeconds: 60}
	kPort1 := DeriveRateLimitKey(KeyInput{OrgID: "org-1", PolicyID: "p1", RouteID: "r1", ClientIP: "198.51.100.7:40001"}, ipPol)
	kPort2 := DeriveRateLimitKey(KeyInput{OrgID: "org-1", PolicyID: "p1", RouteID: "r1", ClientIP: "198.51.100.7:40002"}, ipPol)
	if kPort1 != kPort2 {
		t.Fatalf("same client IP with different ports produced different keys: %s vs %s", kPort1, kPort2)
	}
}
