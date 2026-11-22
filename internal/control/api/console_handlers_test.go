package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func execConsoleReq(t *testing.T, h *ConsoleHandler, payload map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/orgs/00000000-0000-0000-0000-000000000001/console/execute", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.handleExecute(rec, req)
	return rec
}

func TestConsoleHandlerForwardsStrictlyToGatewayAndStripsSpoofedHeaders(t *testing.T) {
	var seenHost, seenPath, seenXFF, seenAPIKey string
	mockGateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenHost = r.Host
		seenPath = r.URL.RequestURI()
		seenXFF = r.Header.Get("X-Forwarded-For")
		seenAPIKey = r.Header.Get("X-Api-Key")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"hello":"gateway"}`))
	}))
	defer mockGateway.Close()

	h := &ConsoleHandler{GatewayBaseURL: mockGateway.URL}
	rec := execConsoleReq(t, h, map[string]any{
		"method": "GET",
		"host":   "api.acme.local",
		"path":   "/v1/orders?limit=5",
		"headers": map[string]string{
			"X-Api-Key":       "gwf_demo.secret",
			"X-Forwarded-For": "1.2.3.4",
		},
		"body": "",
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if seenHost != "api.acme.local" {
		t.Fatalf("expected virtual host api.acme.local, got %q", seenHost)
	}
	if seenPath != "/v1/orders?limit=5" {
		t.Fatalf("expected /v1/orders?limit=5, got %q", seenPath)
	}
	if seenXFF != "" {
		t.Fatalf("expected spoofed X-Forwarded-For to be stripped, got %q", seenXFF)
	}
	if seenAPIKey != "gwf_demo.secret" {
		t.Fatalf("expected X-Api-Key forwarded, got %q", seenAPIKey)
	}
}

func TestConsoleHandlerBlocksSSRFAbuseAndRedirects(t *testing.T) {
	metadataHit := false
	fakeMetadata := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		metadataHit = true
		w.WriteHeader(http.StatusOK)
	}))
	defer fakeMetadata.Close()

	mockGateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect-to-meta" {
			http.Redirect(w, r, fakeMetadata.URL+"/latest/meta-data", http.StatusFound)
			return
		}
		if r.URL.Path == "/binary" {
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write([]byte{0x00, 0x01, 0xff, 0xfe})
			return
		}
		if r.URL.Path == "/huge" {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte(strings.Repeat("A", maxConsoleResponseBodyBytes+1024)))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer mockGateway.Close()

	h := &ConsoleHandler{GatewayBaseURL: mockGateway.URL}

	// SSRF path & host smuggling attempts must be rejected with 422
	badCases := []map[string]any{
		{"method": "GET", "host": "169.254.169.254:80", "path": "/latest"},
		{"method": "GET", "host": "http://127.0.0.1", "path": "/admin"},
		{"method": "GET", "host": "api.local", "path": "http://169.254.169.254/latest/meta-data"},
		{"method": "GET", "host": "api.local", "path": "//169.254.169.254/latest"},
		{"method": "GET", "host": "api.local", "path": "/v1/../../etc/passwd"},
	}
	for _, bc := range badCases {
		rec := execConsoleReq(t, h, bc)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("expected 422 for %v, got %d: %s", bc, rec.Code, rec.Body.String())
		}
	}

	// Redirect must NOT be followed
	rec := execConsoleReq(t, h, map[string]any{"method": "GET", "host": "api.local", "path": "/redirect-to-meta"})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 wrapper for 302, got %d", rec.Code)
	}
	if metadataHit {
		t.Fatal("console transport followed upstream redirect to metadata server")
	}
	var out consoleExecuteResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Status != http.StatusFound {
		t.Fatalf("expected wrapped status 302, got %d", out.Status)
	}

	// Binary detection
	binRec := execConsoleReq(t, h, map[string]any{"method": "GET", "host": "api.local", "path": "/binary"})
	if err := json.Unmarshal(binRec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal binary: %v", err)
	}
	if !out.IsBinary || out.Body != "" {
		t.Fatalf("expected binary flag with empty text preview, got is_binary=%v body=%q", out.IsBinary, out.Body)
	}

	// Oversized response truncation
	hugeRec := execConsoleReq(t, h, map[string]any{"method": "GET", "host": "api.local", "path": "/huge"})
	if err := json.Unmarshal(hugeRec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal huge: %v", err)
	}
	if !out.BodyTruncated || len(out.Body) != maxConsoleResponseBodyBytes {
		t.Fatalf("expected truncated body of %d bytes, got truncated=%v len=%d", maxConsoleResponseBodyBytes, out.BodyTruncated, len(out.Body))
	}
}
