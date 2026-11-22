package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2idParams configures memory, iterations, parallelism, salt, and key length.
type Argon2idParams struct {
	MemoryKiB   uint32
	Iterations  uint32
	Parallelism uint8
	SaltLength  uint32
	KeyLength   uint32
}

// DefaultArgon2idParams returns OWASP-aligned Argon2id defaults.
func DefaultArgon2idParams() Argon2idParams {
	return Argon2idParams{
		MemoryKiB:   64 * 1024, // 64 MiB
		Iterations:  3,
		Parallelism: 2,
		SaltLength:  16,
		KeyLength:   32,
	}
}

// Argon2idHasher implements PasswordHasher using Argon2id with unique salts
// and constant-time digest verification.
type Argon2idHasher struct {
	params Argon2idParams
}

// NewArgon2idHasher constructs an Argon2idHasher with the provided parameters.
func NewArgon2idHasher(params Argon2idParams) *Argon2idHasher {
	if params.MemoryKiB == 0 {
		params = DefaultArgon2idParams()
	}
	return &Argon2idHasher{params: params}
}

// Hash derives a PHC-formatted Argon2id password hash with a fresh random salt.
func (h *Argon2idHasher) Hash(password string) (string, error) {
	if len(password) < 8 || len(password) > 128 {
		return "", errors.New("password length must be between 8 and 128 characters")
	}
	salt := make([]byte, h.params.SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}
	digest := argon2.IDKey(
		[]byte(password),
		salt,
		h.params.Iterations,
		h.params.MemoryKiB,
		h.params.Parallelism,
		h.params.KeyLength,
	)

	b64Salt := base64.RawStdEncoding.EncodeToString(salt)
	b64Digest := base64.RawStdEncoding.EncodeToString(digest)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		h.params.MemoryKiB,
		h.params.Iterations,
		h.params.Parallelism,
		b64Salt,
		b64Digest,
	), nil
}

// Verify checks password against an encoded PHC Argon2id hash in constant time.
func (h *Argon2idHasher) Verify(password, encodedHash string) (bool, error) {
	parts := strings.Split(encodedHash, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return false, errors.New("invalid password hash format")
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, errors.New("unsupported argon2id version")
	}

	var memory uint32
	var iterations uint32
	var parallelism uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &parallelism); err != nil {
		return false, errors.New("invalid argon2id parameters")
	}
	if memory < 8*1024 || iterations < 1 || parallelism < 1 {
		return false, errors.New("unsafe argon2id parameters")
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) < 8 {
		return false, errors.New("invalid argon2id salt")
	}
	expectedDigest, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(expectedDigest) < 16 {
		return false, errors.New("invalid argon2id digest")
	}

	actualDigest := argon2.IDKey([]byte(password), salt, iterations, memory, parallelism, uint32(len(expectedDigest)))
	return subtle.ConstantTimeCompare(actualDigest, expectedDigest) == 1, nil
}

// RedactSensitiveMap recursively masks credential/secret keys in structured payloads.
func RedactSensitiveMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		if IsSensitiveKey(k) {
			out[k] = "[REDACTED]"
			continue
		}
		switch typed := v.(type) {
		case map[string]any:
			out[k] = RedactSensitiveMap(typed)
		case []any:
			arr := make([]any, len(typed))
			for i, item := range typed {
				if sub, ok := item.(map[string]any); ok {
					arr[i] = RedactSensitiveMap(sub)
				} else {
					arr[i] = item
				}
			}
			out[k] = arr
		default:
			out[k] = v
		}
	}
	return out
}

// IsSensitiveKey reports whether a field/header name could carry secrets or credentials.
func IsSensitiveKey(key string) bool {
	lower := strings.ToLower(strings.TrimSpace(key))
	for _, needle := range []string{
		"password",
		"passwd",
		"secret",
		"token",
		"api_key",
		"apikey",
		"key_hash",
		"authorization",
		"cookie",
		"set-cookie",
		"private_key",
		"csrf",
		"credential",
	} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}
