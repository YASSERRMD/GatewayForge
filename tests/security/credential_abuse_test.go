package security

import (
	"bytes"
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gwauth "github.com/YASSERRMD/GatewayForge/internal/gateway/auth"
	"github.com/YASSERRMD/GatewayForge/internal/gateway/table"
	"github.com/YASSERRMD/GatewayForge/internal/shared/apikey"
	"github.com/YASSERRMD/GatewayForge/internal/shared/redact"
	"github.com/YASSERRMD/GatewayForge/internal/shared/snapshot"
)

type staticKeyProvider struct {
	key crypto.PublicKey
	err error
}

func (p staticKeyProvider) GetKey(_ context.Context, _, _ string) (crypto.PublicKey, error) {
	if p.err != nil {
		return nil, p.err
	}
	return p.key, nil
}

func signRS256(t *testing.T, priv *rsa.PrivateKey, header, claims map[string]any) string {
	t.Helper()
	hb, _ := json.Marshal(header)
	cb, _ := json.Marshal(claims)
	hEnc := base64.RawURLEncoding.EncodeToString(hb)
	cEnc := base64.RawURLEncoding.EncodeToString(cb)
	signingInput := hEnc + "." + cEnc
	sum := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatalf("sign RS256: %v", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func TestJWTAlgorithmSubstitutionTamperingAndJWKSOutageFailClosed(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("gen rsa: %v", err)
	}
	now := time.Now().UTC()
	cfg := gwauth.JWTPolicyConfig{
		Issuer:      "https://idp.sec.test/",
		Audience:    "gatewayforge-api",
		JWKSURL:     "https://idp.sec.test/.well-known/jwks.json",
		AllowedAlgs: []string{"RS256"},
	}

	verifier := gwauth.NewJWTVerifier(staticKeyProvider{key: &priv.PublicKey})

	// 0. Valid RS256 token passes
	validTok := signRS256(t, priv, map[string]any{"alg": "RS256", "kid": "k1", "typ": "JWT"}, map[string]any{
		"iss": cfg.Issuer, "aud": cfg.Audience, "sub": "user-1", "exp": now.Add(5 * time.Minute).Unix(),
	})
	if _, err := verifier.VerifyToken(context.Background(), validTok, cfg); err != nil {
		t.Fatalf("expected valid RS256 JWT to pass, got %v", err)
	}

	// 1. alg:none attack MUST be rejected
	hbNone, _ := json.Marshal(map[string]any{"alg": "none", "typ": "JWT"})
	cbValid, _ := json.Marshal(map[string]any{"iss": cfg.Issuer, "aud": cfg.Audience, "sub": "evil", "exp": now.Add(5 * time.Minute).Unix()})
	algNoneTok := base64.RawURLEncoding.EncodeToString(hbNone) + "." + base64.RawURLEncoding.EncodeToString(cbValid) + ".unsigned"
	if _, err := verifier.VerifyToken(context.Background(), algNoneTok, cfg); err == nil {
		t.Fatal("alg:none JWT MUST be rejected")
	}

	// 2. HS256 key-confusion attack (HMAC signed using RSA public key DER bytes) MUST be rejected
	pubDER, _ := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	hbHS, _ := json.Marshal(map[string]any{"alg": "HS256", "kid": "k1", "typ": "JWT"})
	inputHS := base64.RawURLEncoding.EncodeToString(hbHS) + "." + base64.RawURLEncoding.EncodeToString(cbValid)
	mac := hmac.New(sha256.New, pubDER)
	_, _ = mac.Write([]byte(inputHS))
	hs256Tok := inputHS + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if _, err := verifier.VerifyToken(context.Background(), hs256Tok, cfg); err == nil {
		t.Fatal("HS256 algorithm-substitution JWT MUST be rejected")
	}

	// 3. Expired, wrong issuer, wrong audience, and tampered payload MUST be rejected
	badTokens := []struct {
		name string
		tok  string
	}{
		{"expired", signRS256(t, priv, map[string]any{"alg": "RS256", "kid": "k1"}, map[string]any{"iss": cfg.Issuer, "aud": cfg.Audience, "sub": "u", "exp": now.Add(-10 * time.Minute).Unix()})},
		{"wrong_iss", signRS256(t, priv, map[string]any{"alg": "RS256", "kid": "k1"}, map[string]any{"iss": "https://evil.test/", "aud": cfg.Audience, "sub": "u", "exp": now.Add(5 * time.Minute).Unix()})},
		{"wrong_aud", signRS256(t, priv, map[string]any{"alg": "RS256", "kid": "k1"}, map[string]any{"iss": cfg.Issuer, "aud": "other-api", "sub": "u", "exp": now.Add(5 * time.Minute).Unix()})},
		{"tampered", validTok[:len(validTok)-4] + "AAAA"},
	}
	for _, tc := range badTokens {
		if _, err := verifier.VerifyToken(context.Background(), tc.tok, cfg); err == nil {
			t.Fatalf("%s JWT MUST be rejected", tc.name)
		}
	}

	// 4. JWKS outage MUST fail closed
	outageVerifier := gwauth.NewJWTVerifier(staticKeyProvider{err: errors.New("jwks endpoint unreachable")})
	if _, err := outageVerifier.VerifyToken(context.Background(), validTok, cfg); err == nil {
		t.Fatal("JWKS outage MUST fail closed and reject token")
	}
}

func TestAPIKeyRevokedExpiredSuspendedAndLogRedaction(t *testing.T) {
	pepper := []byte("sec-test-pepper-32-bytes-minimum!!")
	activeKey, err := apikey.Generate(pepper)
	if err != nil {
		t.Fatalf("gen active key: %v", err)
	}
	expiredKey, err := apikey.Generate(pepper)
	if err != nil {
		t.Fatalf("gen expired key: %v", err)
	}
	suspendedKey, err := apikey.Generate(pepper)
	if err != nil {
		t.Fatalf("gen suspended key: %v", err)
	}
	revokedKey, err := apikey.Generate(pepper)
	if err != nil {
		t.Fatalf("gen revoked key: %v", err)
	}

	past := time.Now().UTC().Add(-1 * time.Hour)
	snap := snapshot.Snapshot{
		SchemaVersion: snapshot.SchemaVersion,
		OrgID:         "org-cred",
		Version:       1,
		Consumers: []snapshot.Consumer{
			{
				ID:     "c-active",
				Name:   "active-consumer",
				Status: "active",
				Keys: []snapshot.APIKey{
					{ID: "k1", KeyPrefix: activeKey.Prefix, KeyHash: activeKey.Hash, Scopes: []string{"orders:read"}},
					{ID: "k2", KeyPrefix: expiredKey.Prefix, KeyHash: expiredKey.Hash, Scopes: []string{"orders:read"}, ExpiresAt: &past},
				},
			},
			{
				ID:     "c-susp",
				Name:   "suspended-consumer",
				Status: "suspended",
				Keys: []snapshot.APIKey{
					{ID: "k3", KeyPrefix: suspendedKey.Prefix, KeyHash: suspendedKey.Hash, Scopes: []string{"orders:read"}},
				},
			},
		},
	}
	tbl, err := table.Compile(snap, false)
	if err != nil {
		t.Fatalf("compile table: %v", err)
	}

	authN := gwauth.NewAPIKeyAuthenticator(pepper)
	reqWith := func(k string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/v1/orders", nil)
		r.Header.Set("X-Api-Key", k)
		return r
	}

	// Active key authenticates and satisfies orders:read scope
	id, err := authN.Authenticate(reqWith(activeKey.Plaintext), tbl, gwauth.APIKeyPolicyConfig{RequiredScopes: []string{"orders:read"}})
	if err != nil {
		t.Fatalf("expected active key to pass, got %v", err)
	}
	if err := gwauth.AuthorizeScopes(id, []string{"orders:read"}, nil); err != nil {
		t.Fatalf("expected orders:read scope to pass, got %v", err)
	}
	// Missing required scope returns ErrForbidden
	if err := gwauth.AuthorizeScopes(id, []string{"orders:write"}, nil); !errors.Is(err, gwauth.ErrForbidden) {
		t.Fatalf("expected ErrForbidden for insufficient scope, got %v", err)
	}
	// Expired, suspended, revoked (not in snapshot), and tampered keys ALL fail with ErrUnauthorized
	for name, badKey := range map[string]string{
		"expired":   expiredKey.Plaintext,
		"suspended": suspendedKey.Plaintext,
		"revoked":   revokedKey.Plaintext,
		"tampered":  activeKey.Prefix + ".0000000000000000000000000000000000000000000000000000000000000000",
	} {
		if _, err := authN.Authenticate(reqWith(badKey), tbl, gwauth.APIKeyPolicyConfig{}); !errors.Is(err, gwauth.ErrUnauthorized) {
			t.Fatalf("%s key expected ErrUnauthorized, got %v", name, err)
		}
	}

	// AT-18: Verify operator JSON logger never leaks API keys, JWTs, Cookies, or passwords
	var logBuf bytes.Buffer
	logger := redact.NewJSONLogger(&logBuf, nil)
	logger.Info("auth_attempt",
		"api_key", activeKey.Plaintext,
		"authorization", "Bearer eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJ1MSJ9.signature123456",
		"cookie", "gwf_session=supersecretcookie",
		"password", "MySecretPass!2026",
		"note", "client sent "+activeKey.Plaintext,
	)

	logged := logBuf.String()
	if leaks := redact.FindLeaks(logged); len(leaks) > 0 {
		t.Fatalf("operator logs leaked secrets: %v\nlog=%s", leaks, logged)
	}
	if bytes.Contains(logBuf.Bytes(), []byte("supersecretcookie")) || bytes.Contains(logBuf.Bytes(), []byte("MySecretPass!2026")) {
		t.Fatalf("sensitive cookie or password leaked in logs: %s", logged)
	}
}
