package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/YASSERRMD/GatewayForge/internal/shared/redact"
)

func TestStructuredJSONAccessLoggingAndRedaction(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(&buf, "gateway", "test", slog.LevelInfo)

	rawKey := "gwf_1234567890ab.secretpayload000000000000000000000000000000000000000000"
	LogAccess(logger, AccessRecord{
		Timestamp:  time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC),
		RequestID:  "req_abc123 " + rawKey,
		TraceID:    "4bf92f3577b34da6a3ce929d0e0e4736",
		OrgID:      "org-1",
		RouteID:    "rt-orders",
		ServiceID:  "svc-orders",
		Method:     "GET",
		PathPrefix: "/v1/orders",
		Status:     200,
		DurationMS: 4.25,
	})
	LogAccess(logger, AccessRecord{
		RequestID:  "req_err504",
		TraceID:    "4bf92f3577b34da6a3ce929d0e0e4736",
		OrgID:      "org-1",
		RouteID:    "rt-orders",
		ServiceID:  "svc-orders",
		Method:     "GET",
		PathPrefix: "/v1/orders",
		Status:     504,
		DurationMS: 150.1,
	})

	raw := buf.String()
	if leaks := redact.FindLeaks(raw, rawKey); len(leaks) > 0 {
		t.Fatalf("access log leaked secret: %v\n%s", leaks, raw)
	}

	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	if len(lines) != 2 {
		t.Fatalf("expected 2 JSON log lines, got %d", len(lines))
	}

	var first, second map[string]any
	if err := json.Unmarshal(lines[0], &first); err != nil {
		t.Fatalf("line 1 invalid JSON: %v", err)
	}
	if err := json.Unmarshal(lines[1], &second); err != nil {
		t.Fatalf("line 2 invalid JSON: %v", err)
	}
	if first["level"] != "INFO" || first["outcome"] != "ok" || first["service"] != "gateway" || first["route_id"] != "rt-orders" {
		t.Fatalf("unexpected first log fields: %+v", first)
	}
	if second["level"] != "ERROR" || second["outcome"] != "timeout" {
		t.Fatalf("unexpected second log fields: %+v", second)
	}
}
