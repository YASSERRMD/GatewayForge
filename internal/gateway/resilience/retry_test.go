package resilience

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/YASSERRMD/GatewayForge/internal/shared/policy"
)

func TestIdempotentRetryGuardRejectsPOSTAndAllowsGET(t *testing.T) {
	// Schema validation strictly forbids POST / PATCH in retryable_methods.
	if _, err := policy.ParseRetryPolicy(json.RawMessage(`{"max_attempts":3,"retryable_methods":["GET","POST"]}`)); err == nil {
		t.Fatal("expected ParseRetryPolicy to reject POST")
	}
	if _, err := policy.ParseRetryPolicy(json.RawMessage(`{"max_attempts":3,"retryable_methods":["PATCH"]}`)); err == nil {
		t.Fatal("expected ParseRetryPolicy to reject PATCH")
	}

	pol, err := policy.ParseRetryPolicy(json.RawMessage(`{"max_attempts":3,"retryable_methods":["GET","HEAD","PUT"]}`))
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if !CanRetryMethod("GET", pol) || !CanRetryMethod("HEAD", pol) || !CanRetryMethod("PUT", pol) {
		t.Fatal("expected GET, HEAD, PUT to be retryable")
	}
	if CanRetryMethod("POST", pol) || CanRetryMethod("PATCH", pol) || CanRetryMethod("DELETE", pol) {
		t.Fatal("expected POST, PATCH, and unlisted DELETE to be non-retryable")
	}

	// Outcome classification: 503/dial-error retryable; 4xx & context.Canceled non-retryable.
	if !IsRetryableOutcome(http.StatusServiceUnavailable, nil, pol) {
		t.Fatal("503 should be retryable")
	}
	if !IsRetryableOutcome(0, errors.New("connection reset"), pol) {
		t.Fatal("transport error should be retryable")
	}
	if IsRetryableOutcome(http.StatusBadRequest, nil, pol) || IsRetryableOutcome(http.StatusNotFound, nil, pol) {
		t.Fatal("4xx must never be retried")
	}
	if IsRetryableOutcome(0, context.Canceled, pol) || IsRetryableOutcome(0, context.DeadlineExceeded, pol) {
		t.Fatal("canceled or expired context must never be retried")
	}
}

func TestPrepareReplayBodyBoundedBuffer(t *testing.T) {
	small := bytes.Repeat([]byte("a"), 128)
	req1 := httptest.NewRequest(http.MethodPut, "/items/1", bytes.NewReader(small))
	buf, replayable, err := PrepareReplayBody(req1, 256)
	if err != nil || !replayable || len(buf) != 128 {
		t.Fatalf("small body expected replayable=true len=128, got replayable=%v len=%d err=%v", replayable, len(buf), err)
	}

	large := bytes.Repeat([]byte("b"), 512)
	req2 := httptest.NewRequest(http.MethodPut, "/items/1", bytes.NewReader(large))
	_, replayable2, err := PrepareReplayBody(req2, 256)
	if err != nil || replayable2 {
		t.Fatalf("oversized body expected replayable=false, got replayable=%v err=%v", replayable2, err)
	}
	// Restored stream must still contain all 512 original bytes intact!
	restored, _ := io.ReadAll(req2.Body)
	if !bytes.Equal(restored, large) {
		t.Fatalf("restored body mismatch: got %d bytes want 512", len(restored))
	}
}
