package gwauth

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"
)

// JWTPolicyConfig configures strict asymmetric JWT verification for a route/service/org.
type JWTPolicyConfig struct {
	Issuer          string   `json:"issuer"`
	Audience        string   `json:"audience"`
	JWKSURL         string   `json:"jwks_url"`
	AllowedAlgs     []string `json:"allowed_algs,omitempty"`
	RequiredScopes  []string `json:"required_scopes,omitempty"`
	ClockSkewSec    int      `json:"clock_skew_sec,omitempty"`
	StripCredential *bool    `json:"strip_credential,omitempty"`
}

// KeyProvider resolves a verified asymmetric public key by JWKS URL and key ID (kid).
type KeyProvider interface {
	GetKey(ctx context.Context, jwksURL, kid string) (crypto.PublicKey, error)
}

// JWTVerifier validates signed JWT Bearer tokens with explicit issuer/audience/algorithm constraints.
type JWTVerifier struct {
	Keys KeyProvider
	Now  func() time.Time
}

// NewJWTVerifier constructs a JWTVerifier backed by keys.
func NewJWTVerifier(keys KeyProvider) *JWTVerifier {
	return &JWTVerifier{
		Keys: keys,
		Now:  func() time.Time { return time.Now().UTC() },
	}
}

type jwtHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	Typ string `json:"typ"`
}

type jwtClaims struct {
	Iss   string   `json:"iss"`
	Sub   string   `json:"sub"`
	Aud   any      `json:"aud"`
	Exp   *int64   `json:"exp"`
	Nbf   *int64   `json:"nbf"`
	Scope string   `json:"scope"`
	Scp   []string `json:"scp"`
}

// ExtractBearerJWT extracts a raw Bearer JWT token from Authorization header.
func ExtractBearerJWT(r *http.Request) string {
	authz := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(authz) < 8 || !strings.EqualFold(authz[:7], "Bearer ") {
		return ""
	}
	return strings.TrimSpace(authz[7:])
}

// Authenticate verifies the Bearer JWT in r according to cfg and returns a ConsumerIdentity.
func (v *JWTVerifier) Authenticate(r *http.Request, cfg JWTPolicyConfig) (ConsumerIdentity, error) {
	if v == nil || v.Keys == nil {
		return ConsumerIdentity{}, ErrUnauthorized
	}
	raw := ExtractBearerJWT(r)
	if raw == "" || len(raw) > 16384 {
		return ConsumerIdentity{}, ErrUnauthorized
	}
	return v.VerifyToken(r.Context(), raw, cfg)
}

// VerifyToken cryptographically verifies a compact JWS string and validates claims.
func (v *JWTVerifier) VerifyToken(ctx context.Context, token string, cfg JWTPolicyConfig) (ConsumerIdentity, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return ConsumerIdentity{}, ErrUnauthorized
	}

	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return ConsumerIdentity{}, ErrUnauthorized
	}
	var hdr jwtHeader
	if err := json.Unmarshal(headerBytes, &hdr); err != nil {
		return ConsumerIdentity{}, ErrUnauthorized
	}

	// Strictly enforce asymmetric allowlist; ALWAYS reject 'none' and symmetric HS* algorithms.
	if !isAllowedJWTAlg(hdr.Alg, cfg.AllowedAlgs) {
		return ConsumerIdentity{}, ErrUnauthorized
	}
	if strings.TrimSpace(hdr.Kid) == "" {
		return ConsumerIdentity{}, ErrUnauthorized
	}

	pubKey, err := v.Keys.GetKey(ctx, cfg.JWKSURL, hdr.Kid)
	if err != nil || pubKey == nil {
		return ConsumerIdentity{}, ErrUnauthorized
	}

	sigBytes, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return ConsumerIdentity{}, ErrUnauthorized
	}
	signingInput := []byte(parts[0] + "." + parts[1])

	if err := verifyJWSSignature(hdr.Alg, pubKey, signingInput, sigBytes); err != nil {
		return ConsumerIdentity{}, ErrUnauthorized
	}

	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ConsumerIdentity{}, ErrUnauthorized
	}
	var claims jwtClaims
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return ConsumerIdentity{}, ErrUnauthorized
	}

	nowFn := v.Now
	if nowFn == nil {
		nowFn = func() time.Time { return time.Now().UTC() }
	}
	nowUnix := nowFn().Unix()
	skew := int64(cfg.ClockSkewSec)
	if skew < 0 {
		skew = 0
	}
	if skew > 300 {
		skew = 300
	}

	// exp is mandatory.
	if claims.Exp == nil || nowUnix-skew >= *claims.Exp {
		return ConsumerIdentity{}, ErrUnauthorized
	}
	if claims.Nbf != nil && nowUnix+skew < *claims.Nbf {
		return ConsumerIdentity{}, ErrUnauthorized
	}
	if cfg.Issuer != "" && claims.Iss != cfg.Issuer {
		return ConsumerIdentity{}, ErrUnauthorized
	}
	if cfg.Audience != "" && !audienceContains(claims.Aud, cfg.Audience) {
		return ConsumerIdentity{}, ErrUnauthorized
	}

	scopes := extractClaimScopes(claims)
	return ConsumerIdentity{
		Method:       "jwt",
		ConsumerID:   claims.Sub,
		ConsumerName: claims.Sub,
		KeyID:        hdr.Kid,
		Subject:      claims.Sub,
		Scopes:       scopes,
	}, nil
}

func isAllowedJWTAlg(alg string, configured []string) bool {
	// Hard-coded safe asymmetric algorithms only.
	switch alg {
	case "RS256", "ES256":
	default:
		return false
	}
	if len(configured) == 0 {
		return true
	}
	for _, a := range configured {
		if a == alg {
			return true
		}
	}
	return false
}

func verifyJWSSignature(alg string, pubKey crypto.PublicKey, signingInput, sig []byte) error {
	digest := sha256.Sum256(signingInput)
	switch alg {
	case "RS256":
		rsaPub, ok := pubKey.(*rsa.PublicKey)
		if !ok {
			return errors.New("key type mismatch for RS256")
		}
		if rsaPub.N.BitLen() < 2048 {
			return errors.New("RSA key too small (<2048 bits)")
		}
		return rsa.VerifyPKCS1v15(rsaPub, crypto.SHA256, digest[:], sig)
	case "ES256":
		ecPub, ok := pubKey.(*ecdsa.PublicKey)
		if !ok || ecPub.Curve != elliptic.P256() {
			return errors.New("key type mismatch for ES256")
		}
		if len(sig) != 64 {
			return errors.New("invalid ES256 raw signature length")
		}
		r := new(big.Int).SetBytes(sig[:32])
		s := new(big.Int).SetBytes(sig[32:])
		if !ecdsa.Verify(ecPub, digest[:], r, s) {
			return errors.New("invalid ES256 signature")
		}
		return nil
	default:
		return fmt.Errorf("unsupported algorithm %q", alg)
	}
}

func audienceContains(raw any, want string) bool {
	switch v := raw.(type) {
	case string:
		return v == want
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok && s == want {
				return true
			}
		}
	}
	return false
}

func extractClaimScopes(c jwtClaims) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, s := range strings.Fields(c.Scope) {
		if _, dup := seen[s]; !dup {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	for _, s := range c.Scp {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, dup := seen[s]; !dup {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	return out
}
