package gwauth

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/YASSERRMD/GatewayForge/internal/gateway/table"
	"github.com/YASSERRMD/GatewayForge/internal/shared/apikey"
)

var (
	// ErrUnauthorized is returned uniformly for all invalid/missing/expired/revoked credentials
	// to avoid leaking key existence or status.
	ErrUnauthorized = errors.New("authentication credentials are missing or invalid")
	// ErrForbidden is returned when an authenticated consumer lacks required route scopes.
	ErrForbidden = errors.New("authenticated consumer lacks required scope")
)

const dummyKeyHash = "0000000000000000000000000000000000000000000000000000000000000000"

// APIKeyPolicyConfig represents the typed JSON configuration of an api_key policy.
type APIKeyPolicyConfig struct {
	HeaderName      string   `json:"header_name"`
	RequiredScopes  []string `json:"required_scopes,omitempty"`
	StripCredential *bool    `json:"strip_credential,omitempty"`
}

// ConsumerIdentity represents an authenticated caller identity on the gateway data plane.
type ConsumerIdentity struct {
	Method       string
	ConsumerID   string
	ConsumerName string
	KeyID        string
	Subject      string
	Scopes       []string
}

// APIKeyAuthenticator validates consumer API keys against an immutable compiled routing table.
type APIKeyAuthenticator struct {
	Pepper []byte
	Now    func() time.Time
}

// NewAPIKeyAuthenticator constructs an APIKeyAuthenticator.
func NewAPIKeyAuthenticator(pepper []byte) *APIKeyAuthenticator {
	return &APIKeyAuthenticator{
		Pepper: pepper,
		Now:    func() time.Time { return time.Now().UTC() },
	}
}

// ExtractKey extracts a raw API key from the configured header (default X-Api-Key) or Authorization: ApiKey <token>.
func ExtractKey(r *http.Request, cfg APIKeyPolicyConfig) (string, string) {
	header := strings.TrimSpace(cfg.HeaderName)
	if header == "" {
		header = "X-Api-Key"
	}
	if v := strings.TrimSpace(r.Header.Get(header)); v != "" {
		return v, header
	}
	if authz := strings.TrimSpace(r.Header.Get("Authorization")); strings.HasPrefix(strings.ToLower(authz), "apikey ") {
		return strings.TrimSpace(authz[len("apikey "):]), "Authorization"
	}
	return "", header
}

// Authenticate validates the presented API key without leaking prefix presence or revocation reason.
func (a *APIKeyAuthenticator) Authenticate(r *http.Request, tbl *table.CompiledTable, cfg APIKeyPolicyConfig) (ConsumerIdentity, error) {
	raw, _ := ExtractKey(r, cfg)
	if raw == "" || tbl == nil {
		// Perform dummy HMAC to equalize work slightly even on empty/missing lookup.
		_ = apikey.Verify(a.Pepper, "gwf_000000000000.dummy", dummyKeyHash)
		return ConsumerIdentity{}, ErrUnauthorized
	}

	prefix, parsed := apikey.ParsePrefix(raw)
	ref, found := tbl.KeysByPref[prefix]

	// Always run constant-time HMAC verification against either the real hash or a dummy hash
	// so unknown prefixes do not short-circuit earlier than known prefixes.
	targetHash := dummyKeyHash
	if parsed && found {
		targetHash = ref.Key.KeyHash
	}
	matched := apikey.Verify(a.Pepper, raw, targetHash)

	if !parsed || !found || !matched {
		return ConsumerIdentity{}, ErrUnauthorized
	}
	if ref.ConsumerStatus != "active" {
		return ConsumerIdentity{}, ErrUnauthorized
	}
	nowFn := a.Now
	if nowFn == nil {
		nowFn = func() time.Time { return time.Now().UTC() }
	}
	if ref.Key.ExpiresAt != nil && !ref.Key.ExpiresAt.After(nowFn()) {
		return ConsumerIdentity{}, ErrUnauthorized
	}

	return ConsumerIdentity{
		Method:       "api_key",
		ConsumerID:   ref.ConsumerID,
		ConsumerName: ref.ConsumerName,
		KeyID:        ref.Key.ID,
		Subject:      ref.ConsumerID,
		Scopes:       append([]string(nil), ref.Key.Scopes...),
	}, nil
}
