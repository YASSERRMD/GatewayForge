package gwauth

import (
	"context"
	"crypto"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"sync"
	"time"

	"github.com/YASSERRMD/GatewayForge/internal/shared/netsec"
)

const (
	maxJWKSBytes   int64 = 256 << 10 // 256 KiB
	maxKeysPerJWKS int   = 64
	maxCachedJWKS  int   = 64
)

type jwksEntry struct {
	keys      map[string]crypto.PublicKey
	fetchedAt time.Time
	expiresAt time.Time
}

// JWKSCache fetches and caches asymmetric JWK public keys with strict SSRF, size, and TTL bounds.
type JWKSCache struct {
	client          *http.Client
	policy          netsec.TargetPolicy
	ttl             time.Duration
	refreshCooldown time.Duration
	now             func() time.Time

	mu      sync.RWMutex
	entries map[string]*jwksEntry
}

// NewJWKSCache constructs a bounded SSRF-safe JWKSCache.
func NewJWKSCache(transport http.RoundTripper, pol netsec.TargetPolicy, ttl time.Duration) *JWKSCache {
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   5 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("redirects are forbidden for JWKS fetches")
		},
	}
	return &JWKSCache{
		client:          client,
		policy:          pol,
		ttl:             ttl,
		refreshCooldown: 10 * time.Second,
		now:             func() time.Time { return time.Now().UTC() },
		entries:         make(map[string]*jwksEntry),
	}
}

// GetKey resolves kid from jwksURL, refreshing on expiry or unknown kid (subject to cooldown).
// Fails closed if JWKS is unreachable, oversized, blocked by SSRF policy, or missing kid.
func (c *JWKSCache) GetKey(ctx context.Context, jwksURL, kid string) (crypto.PublicKey, error) {
	if jwksURL == "" || kid == "" {
		return nil, errors.New("jwks_url and kid are required")
	}
	if _, err := netsec.ValidateTargetURL(jwksURL, c.policy); err != nil {
		return nil, fmt.Errorf("jwks destination blocked: %w", err)
	}

	now := c.now()
	c.mu.RLock()
	entry, exists := c.entries[jwksURL]
	if exists && now.Before(entry.expiresAt) {
		if k, ok := entry.keys[kid]; ok {
			c.mu.RUnlock()
			return k, nil
		}
		// Unknown kid while cache is still fresh: allow refresh only if cooldown elapsed.
		if now.Sub(entry.fetchedAt) < c.refreshCooldown {
			c.mu.RUnlock()
			return nil, fmt.Errorf("unknown kid %q (refresh cooldown active)", kid)
		}
	}
	c.mu.RUnlock()

	keys, err := c.fetchJWKS(ctx, jwksURL)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	if len(c.entries) >= maxCachedJWKS {
		// Evict oldest entry when cache reaches cap.
		var oldestURL string
		var oldestTime time.Time
		for u, e := range c.entries {
			if oldestURL == "" || e.fetchedAt.Before(oldestTime) {
				oldestURL = u
				oldestTime = e.fetchedAt
			}
		}
		if oldestURL != "" {
			delete(c.entries, oldestURL)
		}
	}
	c.entries[jwksURL] = &jwksEntry{
		keys:      keys,
		fetchedAt: now,
		expiresAt: now.Add(c.ttl),
	}
	c.mu.Unlock()

	k, ok := keys[kid]
	if !ok {
		return nil, fmt.Errorf("kid %q not present in JWKS", kid)
	}
	return k, nil
}

type jwksDocument struct {
	Keys []jwkKey `json:"keys"`
}

type jwkKey struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

func (c *JWKSCache) fetchJWKS(ctx context.Context, jwksURL string) (map[string]crypto.PublicKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jwksURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch JWKS: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("JWKS endpoint returned HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxJWKSBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read JWKS response: %w", err)
	}
	if int64(len(raw)) > maxJWKSBytes {
		return nil, errors.New("JWKS payload exceeds 256 KiB safety cap")
	}

	var doc jwksDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("decode JWKS JSON: %w", err)
	}
	if len(doc.Keys) == 0 || len(doc.Keys) > maxKeysPerJWKS {
		return nil, fmt.Errorf("JWKS key count %d out of bounds (1..%d)", len(doc.Keys), maxKeysPerJWKS)
	}

	parsed := make(map[string]crypto.PublicKey, len(doc.Keys))
	for _, k := range doc.Keys {
		if k.Kid == "" {
			continue
		}
		if k.Use != "" && k.Use != "sig" {
			continue
		}
		pub, err := parseJWKPublicKey(k)
		if err != nil {
			continue
		}
		parsed[k.Kid] = pub
	}
	if len(parsed) == 0 {
		return nil, errors.New("JWKS contained no usable asymmetric verification keys")
	}
	return parsed, nil
}

func parseJWKPublicKey(k jwkKey) (crypto.PublicKey, error) {
	switch k.Kty {
	case "RSA":
		nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
		if err != nil {
			return nil, err
		}
		eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
		if err != nil {
			return nil, err
		}
		n := new(big.Int).SetBytes(nBytes)
		if n.BitLen() < 2048 || n.BitLen() > 8192 {
			return nil, errors.New("RSA key size out of safe bounds (2048..8192)")
		}
		e := int(new(big.Int).SetBytes(eBytes).Int64())
		if e < 3 {
			return nil, errors.New("invalid RSA exponent")
		}
		return &rsa.PublicKey{N: n, E: e}, nil
	case "EC":
		if k.Crv != "P-256" {
			return nil, errors.New("unsupported EC curve")
		}
		xBytes, err := base64.RawURLEncoding.DecodeString(k.X)
		if err != nil {
			return nil, err
		}
		yBytes, err := base64.RawURLEncoding.DecodeString(k.Y)
		if err != nil {
			return nil, err
		}
		if len(xBytes) > 32 || len(yBytes) > 32 {
			return nil, errors.New("EC coordinate too large for P-256")
		}
		uncompressed := make([]byte, 65)
		uncompressed[0] = 0x04
		copy(uncompressed[33-len(xBytes):33], xBytes)
		copy(uncompressed[65-len(yBytes):65], yBytes)
		if _, err := ecdh.P256().NewPublicKey(uncompressed); err != nil {
			return nil, errors.New("EC point not on P-256 curve")
		}
		x := new(big.Int).SetBytes(xBytes)
		y := new(big.Int).SetBytes(yBytes)
		return &ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}, nil
	default:
		return nil, errors.New("unsupported JWK kty")
	}
}

// MarshalRSAPublicJWK builds a JWK entry map from an RSA public key (helper for tests and fixtures).
func MarshalRSAPublicJWK(kid string, pub *rsa.PublicKey) map[string]string {
	eBytes := big.NewInt(int64(pub.E)).Bytes()
	return map[string]string{
		"kty": "RSA",
		"use": "sig",
		"alg": "RS256",
		"kid": kid,
		"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString(eBytes),
	}
}
