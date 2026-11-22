package gwauth

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/YASSERRMD/GatewayForge/internal/gateway/table"
	"github.com/YASSERRMD/GatewayForge/internal/shared/apikey"
	"github.com/YASSERRMD/GatewayForge/internal/shared/snapshot"
)

func TestAPIKeyAuthenticatorSecurityAndUniformErrors(t *testing.T) {
	pepper := []byte("gw-auth-test-pepper")
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)

	activeKey, _ := apikey.Generate(pepper)
	expiredKey, _ := apikey.Generate(pepper)
	suspendedKey, _ := apikey.Generate(pepper)

	past := now.Add(-1 * time.Minute)
	future := now.Add(1 * time.Hour)

	compiled, err := table.Compile(snapshot.Snapshot{
		SchemaVersion: snapshot.SchemaVersion,
		OrgID:         "org-1",
		Version:       1,
		Consumers: []snapshot.Consumer{
			{
				ID: "c-active", Name: "active-app", Status: "active",
				Keys: []snapshot.APIKey{
					{ID: "k-valid", KeyPrefix: activeKey.Prefix, KeyHash: activeKey.Hash, Scopes: []string{"orders:read"}, ExpiresAt: &future},
					{ID: "k-expired", KeyPrefix: expiredKey.Prefix, KeyHash: expiredKey.Hash, Scopes: []string{"orders:read"}, ExpiresAt: &past},
				},
			},
			{
				ID: "c-susp", Name: "susp-app", Status: "suspended",
				Keys: []snapshot.APIKey{
					{ID: "k-susp", KeyPrefix: suspendedKey.Prefix, KeyHash: suspendedKey.Hash, Scopes: []string{"orders:read"}},
				},
			},
		},
	}, false)
	if err != nil {
		t.Fatalf("compile table: %v", err)
	}

	authenticator := NewAPIKeyAuthenticator(pepper)
	authenticator.Now = func() time.Time { return now }

	t.Run("valid active key authenticates", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/orders", nil)
		req.Header.Set("X-Api-Key", activeKey.Plaintext)
		id, err := authenticator.Authenticate(req, compiled, APIKeyPolicyConfig{})
		if err != nil {
			t.Fatalf("expected success, got %v", err)
		}
		if id.ConsumerID != "c-active" || id.KeyID != "k-valid" {
			t.Fatalf("unexpected identity: %+v", id)
		}
	})

	t.Run("custom header name works", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/orders", nil)
		req.Header.Set("X-Partner-Key", activeKey.Plaintext)
		if _, err := authenticator.Authenticate(req, compiled, APIKeyPolicyConfig{HeaderName: "X-Partner-Key"}); err != nil {
			t.Fatalf("expected custom header auth success, got %v", err)
		}
	})

	t.Run("unknown prefix, wrong secret, expired key, and suspended consumer all return uniform ErrUnauthorized", func(t *testing.T) {
		badKeys := []string{
			"",
			"gwf_deadbeef1234.0000000000000000000000000000000000000000000000000000000000000000",
			activeKey.Prefix + ".wrongsecret000000000000000000000000000000000000000000000000000000",
			expiredKey.Plaintext,
			suspendedKey.Plaintext,
		}
		for _, k := range badKeys {
			req := httptest.NewRequest(http.MethodGet, "/orders", nil)
			if k != "" {
				req.Header.Set("X-Api-Key", k)
			}
			_, err := authenticator.Authenticate(req, compiled, APIKeyPolicyConfig{})
			if !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("expected uniform ErrUnauthorized for bad key, got %v", err)
			}
		}
	})
}
