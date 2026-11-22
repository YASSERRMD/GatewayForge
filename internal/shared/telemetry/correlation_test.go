package telemetry

import (
	"testing"
)

func TestBuildTraceViewerURLAndRequestIDValidation(t *testing.T) {
	const traceID = "4bf92f3577b34da6a3ce929d0e0e4736"

	link, ok := BuildTraceViewerURL("http://localhost:16686/trace", traceID)
	if !ok || link != "http://localhost:16686/trace/"+traceID {
		t.Fatalf("unexpected trace link: ok=%v link=%q", ok, link)
	}

	// Empty viewer base URL -> explicit unavailable (false).
	if _, ok := BuildTraceViewerURL("", traceID); ok {
		t.Fatal("expected empty baseURL to return ok=false")
	}
	// Reject javascript: or invalid scheme XSS payloads.
	if _, ok := BuildTraceViewerURL("javascript:alert(1)", traceID); ok {
		t.Fatal("expected javascript: scheme to be rejected")
	}
	// Reject malformed trace ID.
	if _, ok := BuildTraceViewerURL("http://localhost:16686/trace", "../evil"); ok {
		t.Fatal("expected invalid trace ID to be rejected")
	}

	if !IsSafeRequestID("req_abc123-def.456") {
		t.Fatal("expected valid request ID to pass")
	}
	if IsSafeRequestID("../etc/passwd") || IsSafeRequestID("a") {
		t.Fatal("expected malformed request ID to fail")
	}
}
