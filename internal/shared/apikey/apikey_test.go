package apikey

import (
	"strings"
	"testing"
)

func TestAPIKeyGenerationAndConstantTimeVerify(t *testing.T) {
	pepper := []byte("unit-test-pepper-secret-0123456789")

	k1, err := Generate(pepper)
	if err != nil {
		t.Fatalf("Generate k1: %v", err)
	}
	k2, err := Generate(pepper)
	if err != nil {
		t.Fatalf("Generate k2: %v", err)
	}
	if k1.Plaintext == k2.Plaintext || k1.Prefix == k2.Prefix || k1.Hash == k2.Hash {
		t.Fatal("expected distinct high-entropy keys")
	}
	if !strings.HasPrefix(k1.Prefix, "gwf_") || len(k1.Hash) != 64 {
		t.Fatalf("unexpected key format: prefix=%q hash_len=%d", k1.Prefix, len(k1.Hash))
	}

	parsed, ok := ParsePrefix(k1.Plaintext)
	if !ok || parsed != k1.Prefix {
		t.Fatalf("ParsePrefix failed: got %q ok=%v want %q", parsed, ok, k1.Prefix)
	}
	if !Verify(pepper, k1.Plaintext, k1.Hash) {
		t.Fatal("expected valid key to verify")
	}
	if Verify(pepper, k1.Plaintext+"x", k1.Hash) {
		t.Fatal("tampered key must not verify")
	}
	if Verify([]byte("other-pepper"), k1.Plaintext, k1.Hash) {
		t.Fatal("wrong pepper must not verify")
	}
}
