package redact

import (
	"bytes"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestHeaderAndStringRedactionAndLeakScanner(t *testing.T) {
	rawKey := "gwf_a1b2c3d4e5f6.0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	rawJWT := "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJ1c2VyLTEifQ.c2lnbmF0dXJlMTIzNDU2"
	rawCookie := "gwf_session=supersecretsessiontokenvalue"

	hdrs := http.Header{}
	hdrs.Set("Authorization", "Bearer "+rawJWT)
	hdrs.Set("X-Api-Key", rawKey)
	hdrs.Set("Cookie", rawCookie)
	hdrs.Set("X-Csrf-Token", "csrf-secret-value")
	hdrs.Set("X-Request-Id", "req-12345")
	hdrs.Set("X-Custom-Note", "saw "+rawKey+" inline")

	cleanHdrs := Headers(hdrs)
	if cleanHdrs["Authorization"][0] != RedactedValue {
		t.Fatalf("Authorization not redacted: %v", cleanHdrs["Authorization"])
	}
	if cleanHdrs["X-Api-Key"][0] != RedactedValue {
		t.Fatalf("X-Api-Key not redacted: %v", cleanHdrs["X-Api-Key"])
	}
	if cleanHdrs["Cookie"][0] != RedactedValue {
		t.Fatalf("Cookie not redacted: %v", cleanHdrs["Cookie"])
	}
	if cleanHdrs["X-Request-Id"][0] != "req-12345" {
		t.Fatalf("non-sensitive X-Request-Id should be preserved: %v", cleanHdrs["X-Request-Id"])
	}
	if strings.Contains(cleanHdrs["X-Custom-Note"][0], rawKey) {
		t.Fatalf("inline API key leaked in custom header: %v", cleanHdrs["X-Custom-Note"])
	}

	var buf bytes.Buffer
	logger := NewJSONLogger(&buf, nil)
	logger.Error("auth failed for "+rawKey,
		"authorization", "Bearer "+rawJWT,
		"headers", hdrs,
		"err", errors.New("rejected token "+rawJWT),
		"request_id", "req-12345",
		"key_prefix", "gwf_a1b2c3d4",
	)

	logged := buf.String()
	leaks := FindLeaks(logged, rawKey, rawJWT, rawCookie, "csrf-secret-value")
	if len(leaks) > 0 {
		t.Fatalf("detected secret leaks in structured log: %v\nlog=%s", leaks, logged)
	}
	if !strings.Contains(logged, "req-12345") || !strings.Contains(logged, "gwf_a1b2c3d4") {
		t.Fatalf("expected safe correlation fields to remain in log: %s", logged)
	}

	// Verify FindLeaks catches actual leaks reliably.
	if found := FindLeaks("oops leaked "+rawKey, rawKey); len(found) == 0 {
		t.Fatal("expected FindLeaks to detect raw API key")
	}
	if found := FindLeaks("oops leaked " + rawJWT); len(found) == 0 {
		t.Fatal("expected FindLeaks to detect raw JWT")
	}
}
