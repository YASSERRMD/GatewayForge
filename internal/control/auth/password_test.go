package auth

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestArgon2idHasher(t *testing.T) {
	hasher := NewArgon2idHasher(Argon2idParams{
		MemoryKiB:   16 * 1024, // fast but real in unit tests
		Iterations:  1,
		Parallelism: 1,
		SaltLength:  16,
		KeyLength:   32,
	})

	h1, err := hasher.Hash("CorrectHorseBatteryStaple!42")
	if err != nil {
		t.Fatalf("Hash failed: %v", err)
	}
	h2, err := hasher.Hash("CorrectHorseBatteryStaple!42")
	if err != nil {
		t.Fatalf("second Hash failed: %v", err)
	}
	if h1 == h2 {
		t.Fatal("expected unique salts to produce distinct hashes for identical passwords")
	}
	if !strings.HasPrefix(h1, "$argon2id$v=19$") {
		t.Fatalf("unexpected PHC hash prefix: %q", h1)
	}

	ok, err := hasher.Verify("CorrectHorseBatteryStaple!42", h1)
	if err != nil || !ok {
		t.Fatalf("expected valid password verification, got ok=%v err=%v", ok, err)
	}

	ok, err = hasher.Verify("WrongPassword!42", h1)
	if err != nil || ok {
		t.Fatalf("expected false for wrong password, got ok=%v err=%v", ok, err)
	}

	// Corrupt/tampered hash format must return error.
	if _, err := hasher.Verify("anything", "$bcrypt$invalid"); err == nil {
		t.Fatal("expected error for non-argon2id hash")
	}
}

func TestRedactSensitiveMap(t *testing.T) {
	raw := map[string]any{
		"email":         "owner@acme.test",
		"password":      "SuperSecretPass!99",
		"password_hash": "$argon2id$v=19$...",
		"nested": map[string]any{
			"api_key":       "gwf_live_secret_12345",
			"authorization": "Bearer eyJ...",
			"safe_field":    "visible",
		},
	}

	redacted := RedactSensitiveMap(raw)
	encoded, err := json.Marshal(redacted)
	if err != nil {
		t.Fatalf("marshal redacted: %v", err)
	}
	s := string(encoded)
	for _, secret := range []string{"SuperSecretPass!99", "$argon2id$", "gwf_live_secret_12345", "Bearer eyJ"} {
		if strings.Contains(s, secret) {
			t.Fatalf("redacted JSON leaked secret %q: %s", secret, s)
		}
	}
	if !strings.Contains(s, "owner@acme.test") || !strings.Contains(s, "visible") {
		t.Fatalf("redacted JSON dropped safe fields: %s", s)
	}
}
