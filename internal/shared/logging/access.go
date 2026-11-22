package logging

import (
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/YASSERRMD/GatewayForge/internal/shared/redact"
)

// AccessRecord captures the standardized, secret-free structured access log schema.
// Request/response payloads, query strings, cookies, API keys, and tokens are NEVER included.
type AccessRecord struct {
	Timestamp  time.Time
	Service    string
	RequestID  string
	TraceID    string
	OrgID      string
	RouteID    string
	ServiceID  string
	Method     string
	PathPrefix string // configured route prefix (or "[unmatched]"), never raw user query/params
	Status     int
	Outcome    string // ok | client_error | auth_denied | rate_limited | upstream_error | timeout | canceled
	DurationMS float64
}

// NewLogger creates a redacted structured JSON logger stamped with service and environment attributes.
func NewLogger(w io.Writer, service, env string, level slog.Level) *slog.Logger {
	base := redact.NewJSONLogger(w, &slog.HandlerOptions{Level: level})
	return base.With("service", service, "env", env)
}

// ClassifyOutcome maps an HTTP status code to a bounded canonical outcome label.
func ClassifyOutcome(status int) string {
	switch {
	case status >= 200 && status < 400:
		return "ok"
	case status == 401 || status == 403:
		return "auth_denied"
	case status == 429:
		return "rate_limited"
	case status == 499:
		return "canceled"
	case status == 504:
		return "timeout"
	case status >= 500:
		return "upstream_error"
	default:
		return "client_error"
	}
}

// LogAccess emits one structured JSON access log entry with severity derived from status.
func LogAccess(logger *slog.Logger, rec AccessRecord) {
	if logger == nil {
		return
	}
	outcome := rec.Outcome
	if outcome == "" {
		outcome = ClassifyOutcome(rec.Status)
	}
	prefix := strings.TrimSpace(rec.PathPrefix)
	if prefix == "" {
		prefix = "[unmatched]"
	}
	ts := rec.Timestamp
	if ts.IsZero() {
		ts = time.Now().UTC()
	}

	level := slog.LevelInfo
	if rec.Status >= 500 {
		level = slog.LevelError
	} else if rec.Status >= 400 {
		level = slog.LevelWarn
	}

	attrs := []any{
		"ts", ts.UTC().Format(time.RFC3339Nano),
		"request_id", redact.String(rec.RequestID),
		"trace_id", redact.String(rec.TraceID),
		"org_id", rec.OrgID,
		"route_id", rec.RouteID,
		"service_id", rec.ServiceID,
		"method", rec.Method,
		"route_prefix", prefix,
		"status", rec.Status,
		"outcome", outcome,
		"duration_ms", rec.DurationMS,
	}
	switch level {
	case slog.LevelError:
		logger.Error("gateway_request", attrs...)
	case slog.LevelWarn:
		logger.Warn("gateway_request", attrs...)
	default:
		logger.Info("gateway_request", attrs...)
	}
}
