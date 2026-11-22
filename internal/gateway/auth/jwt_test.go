package gwauth

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

type staticKeyProvider map[string]crypto.PublicKey

func (s staticKeyProvider) GetKey(_ context.Context, _, kid string) (crypto.PublicKey, error) {
	k, ok := s[kid]
	if !ok {
		return nil, errors.New("kid not found")
	}
	return k, nil
}

func signTestRS256(t *testing.T, priv *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	hdrJSON, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": kid, "typ": "JWT"})
	payJSON, _ := json.Marshal(claims)
	h := base64.RawURLEncoding.EncodeToString(hdrJSON)
	p := base64.RawURLEncoding.EncodeToString(payJSON)
	digest := sha256.Sum256([]byte(h + "." + p))
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign RS256: %v", err)
	}
	return h + "." + p + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func signTestES256(t *testing.T, priv *ecdsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	hdrJSON, _ := json.Marshal(map[string]string{"alg": "ES256", "kid": kid, "typ": "JWT"})
	payJSON, _ := json.Marshal(claims)
	h := base64.RawURLEncoding.EncodeToString(hdrJSON)
	p := base64.RawURLEncoding.EncodeToString(payJSON)
	digest := sha256.Sum256([]byte(h + "." + p))
	r, s, err := ecdsa.Sign(rand.Reader, priv, digest[:])
	if err != nil {
		t.Fatalf("sign ES256: %v", err)
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return h + "." + p + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func TestJWTVerifierSecuritySuite(t *testing.T) {
	rsaPriv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("gen rsa: %v", err)
	}
	ecPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("gen ecdsa: %v", err)
	}

	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	verifier := NewJWTVerifier(staticKeyProvider{
		"rsa-1": &rsaPriv.PublicKey,
		"ec-1":  &ecPriv.PublicKey,
	})
	verifier.Now = func() time.Time { return now }

	cfg := JWTPolicyConfig{
		Issuer:      "https://idp.gatewayforge.local",
		Audience:    "orders-api",
		JWKSURL:     "http://mock-jwks/.well-known/jwks.json",
		AllowedAlgs: []string{"RS256", "ES256"},
	}

	t.Run("valid RS256 and ES256 tokens verify", func(t *testing.T) {
		claims := map[string]any{
			"iss":   cfg.Issuer,
			"aud":   []string{"other", cfg.Audience},
			"sub":   "svc-checkout",
			"exp":   now.Add(10 * time.Minute).Unix(),
			"scope": "orders:read orders:write",
		}
		tokRSA := signTestRS256(t, rsaPriv, "rsa-1", claims)
		id, err := verifier.VerifyToken(context.Background(), tokRSA, cfg)
		if err != nil || id.Subject != "svc-checkout" || len(id.Scopes) != 2 {
			t.Fatalf("RS256 verify failed: id=%+v err=%v", id, err)
		}

		tokEC := signTestES256(t, ecPriv, "ec-1", claims)
		id2, err := verifier.VerifyToken(context.Background(), tokEC, cfg)
		if err != nil || id2.Subject != "svc-checkout" {
			t.Fatalf("ES256 verify failed: id=%+v err=%v", id2, err)
		}
	})

	t.Run("rejects alg=none attack", func(t *testing.T) {
		h := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","kid":"rsa-1"}`))
		p := base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"https://idp.gatewayforge.local","aud":"orders-api","sub":"hacker","exp":9999999999}`))
		if _, err := verifier.VerifyToken(context.Background(), h+"."+p+".", cfg); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("expected ErrUnauthorized for alg=none, got %v", err)
		}
	})

	t.Run("rejects HS256 algorithm confusion signed with RSA public key", func(t *testing.T) {
		pubBytes, _ := x509.MarshalPKIXPublicKey(&rsaPriv.PublicKey)
		h := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","kid":"rsa-1"}`))
		p := base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"https://idp.gatewayforge.local","aud":"orders-api","sub":"hacker","exp":9999999999}`))
		mac := hmac.New(sha256.New, pubBytes)
		mac.Write([]byte(h + "." + p))
		tok := h + "." + p + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
		if _, err := verifier.VerifyToken(context.Background(), tok, cfg); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("expected ErrUnauthorized for HS256 confusion attack, got %v", err)
		}
	})

	t.Run("rejects tampered signature, wrong iss, wrong aud, and expired exp", func(t *testing.T) {
		valid := signTestRS256(t, rsaPriv, "rsa-1", map[string]any{
			"iss": cfg.Issuer, "aud": cfg.Audience, "sub": "u1", "exp": now.Add(5 * time.Minute).Unix(),
		})
		tampered := valid[:len(valid)-2] + "AA"
		if _, err := verifier.VerifyToken(context.Background(), tampered, cfg); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("expected ErrUnauthorized for tampered signature, got %v", err)
		}

		wrongIss := signTestRS256(t, rsaPriv, "rsa-1", map[string]any{
			"iss": "https://evil.example", "aud": cfg.Audience, "sub": "u1", "exp": now.Add(5 * time.Minute).Unix(),
		})
		if _, err := verifier.VerifyToken(context.Background(), wrongIss, cfg); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("expected ErrUnauthorized for wrong iss, got %v", err)
		}

		wrongAud := signTestRS256(t, rsaPriv, "rsa-1", map[string]any{
			"iss": cfg.Issuer, "aud": "wrong-api", "sub": "u1", "exp": now.Add(5 * time.Minute).Unix(),
		})
		if _, err := verifier.VerifyToken(context.Background(), wrongAud, cfg); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("expected ErrUnauthorized for wrong aud, got %v", err)
		}

		expired := signTestRS256(t, rsaPriv, "rsa-1", map[string]any{
			"iss": cfg.Issuer, "aud": cfg.Audience, "sub": "u1", "exp": now.Add(-1 * time.Minute).Unix(),
		})
		if _, err := verifier.VerifyToken(context.Background(), expired, cfg); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("expected ErrUnauthorized for expired JWT, got %v", err)
		}
	})
}
