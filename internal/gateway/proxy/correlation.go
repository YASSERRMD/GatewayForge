package proxy

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"regexp"
	"strings"
)

type gwCtxKey string

const correlationCtxKey gwCtxKey = "gw_correlation"

var traceparentRe = regexp.MustCompile(`^00-([0-9a-f]{32})-([0-9a-f]{16})-([0-9a-f]{2})$`)

// Correlation carries validated request and W3C trace correlation identifiers.
type Correlation struct {
	RequestID   string
	TraceID     string
	Traceparent string
	Tracestate  string
}

// CorrelationFromContext retrieves Correlation from ctx if present.
func CorrelationFromContext(ctx context.Context) Correlation {
	if c, ok := ctx.Value(correlationCtxKey).(Correlation); ok {
		return c
	}
	return Correlation{}
}

// ResolveRequestID returns raw if it is a safe correlation ID, otherwise generates a new gw_ ID.
func ResolveRequestID(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if len(trimmed) >= 4 && len(trimmed) <= 128 && isSafeCorrelationID(trimmed) {
		return trimmed
	}
	var b [16]byte
	_, _ = rand.Read(b[:])
	return "gw_" + hex.EncodeToString(b[:])
}

func isSafeCorrelationID(s string) bool {
	for _, ch := range s {
		valid := (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_' || ch == '.'
		if !valid {
			return false
		}
	}
	return true
}

// ResolveTraceparent validates an incoming W3C traceparent header or generates a new one.
func ResolveTraceparent(raw string) (traceparent, traceID string) {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	if m := traceparentRe.FindStringSubmatch(trimmed); len(m) == 4 {
		tid, sid := m[1], m[2]
		if !isAllZeroHex(tid) && !isAllZeroHex(sid) {
			return trimmed, tid
		}
	}
	var tidBytes [16]byte
	var sidBytes [8]byte
	_, _ = rand.Read(tidBytes[:])
	_, _ = rand.Read(sidBytes[:])
	tid := hex.EncodeToString(tidBytes[:])
	sid := hex.EncodeToString(sidBytes[:])
	return "00-" + tid + "-" + sid + "-01", tid
}

func isAllZeroHex(s string) bool {
	for _, ch := range s {
		if ch != '0' {
			return false
		}
	}
	return true
}

func sanitizeTracestate(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || len(trimmed) > 512 || strings.ContainsAny(trimmed, "\r\n") {
		return ""
	}
	return trimmed
}

// AttachCorrelation resolves correlation headers, writes X-Request-Id on w, and stores Correlation in ctx.
func AttachCorrelation(w http.ResponseWriter, r *http.Request) (*http.Request, Correlation) {
	reqID := ResolveRequestID(r.Header.Get("X-Request-Id"))
	tp, tid := ResolveTraceparent(r.Header.Get("Traceparent"))
	ts := sanitizeTracestate(r.Header.Get("Tracestate"))
	c := Correlation{
		RequestID:   reqID,
		TraceID:     tid,
		Traceparent: tp,
		Tracestate:  ts,
	}
	w.Header().Set("X-Request-Id", reqID)
	return r.WithContext(context.WithValue(r.Context(), correlationCtxKey, c)), c
}

// ApplyCorrelationOutbound sets validated correlation and W3C trace headers on the outbound upstream request.
func ApplyCorrelationOutbound(out *http.Request, c Correlation) {
	if c.RequestID != "" {
		out.Header.Set("X-Request-Id", c.RequestID)
	}
	if c.Traceparent != "" {
		out.Header.Set("Traceparent", c.Traceparent)
	}
	if c.Tracestate != "" {
		out.Header.Set("Tracestate", c.Tracestate)
	} else {
		out.Header.Del("Tracestate")
	}
}
