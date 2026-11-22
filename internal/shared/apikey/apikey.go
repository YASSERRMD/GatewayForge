package apikey

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"strings"
)

// DefaultPepper is used only when no explicit API-key pepper is configured in non-production dev/test.
var DefaultPepper = []byte("gatewayforge-dev-api-key-pepper-v1")

// IssuedKey contains the one-time plaintext token alongside its non-secret prefix and keyed hash.
type IssuedKey struct {
	Plaintext string
	Prefix    string
	Hash      string
}

// Generate creates a cryptographically random 256-bit API key with a lookup prefix (`gwf_<12hex>`).
func Generate(pepper []byte) (IssuedKey, error) {
	if len(pepper) == 0 {
		pepper = DefaultPepper
	}
	var prefixBytes [6]byte // 12 hex chars
	if _, err := rand.Read(prefixBytes[:]); err != nil {
		return IssuedKey{}, fmt.Errorf("generate key prefix: %w", err)
	}
	var secretBytes [32]byte // 256 bits of entropy
	if _, err := rand.Read(secretBytes[:]); err != nil {
		return IssuedKey{}, fmt.Errorf("generate key secret: %w", err)
	}
	prefix := "gwf_" + hex.EncodeToString(prefixBytes[:])
	plaintext := prefix + "." + hex.EncodeToString(secretBytes[:])
	return IssuedKey{
		Plaintext: plaintext,
		Prefix:    prefix,
		Hash:      Hash(pepper, plaintext),
	}, nil
}

// Hash computes the keyed HMAC-SHA256 hex digest of plaintext.
func Hash(pepper []byte, plaintext string) string {
	if len(pepper) == 0 {
		pepper = DefaultPepper
	}
	mac := hmac.New(sha256.New, pepper)
	mac.Write([]byte(plaintext))
	return hex.EncodeToString(mac.Sum(nil))
}

// ParsePrefix extracts the non-secret prefix (`gwf_<hex>`) from a raw API key.
func ParsePrefix(raw string) (string, bool) {
	prefix, secret, ok := strings.Cut(strings.TrimSpace(raw), ".")
	if !ok || !strings.HasPrefix(prefix, "gwf_") || len(prefix) < 12 || len(prefix) > 24 || len(secret) < 32 {
		return "", false
	}
	return prefix, true
}

// Verify checks plaintext against expectedHash in constant time.
func Verify(pepper []byte, plaintext, expectedHash string) bool {
	computed := Hash(pepper, plaintext)
	return subtle.ConstantTimeCompare([]byte(computed), []byte(expectedHash)) == 1
}
