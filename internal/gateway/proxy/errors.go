package proxy

import (
	"encoding/json"
	"net/http"

	"github.com/YASSERRMD/GatewayForge/internal/shared/redact"
)

const (
	CodeRouteNotFound       = "route_not_found"
	CodeMethodNotAllowed    = "method_not_allowed"
	CodeRequestInvalid      = "request_invalid"
	CodePayloadTooLarge     = "payload_too_large"
	CodeUnauthorized        = "unauthorized"
	CodeForbidden           = "forbidden"
	CodeRateLimited         = "rate_limited"
	CodeUpstreamBlocked     = "upstream_blocked"
	CodeUpstreamUnavailable = "upstream_unavailable"
	CodeUpstreamTimeout     = "upstream_timeout"
	CodeBadGateway          = "bad_gateway"
	CodeServiceOverloaded   = "service_overloaded"
)

// GatewayError is the stable machine-readable JSON error envelope emitted by the gateway data plane.
type GatewayError struct {
	Status    int    `json:"status"`
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
	TraceID   string `json:"trace_id,omitempty"`
}

// WriteGatewayError writes a standardized JSON gateway error including correlation identifiers.
func WriteGatewayError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	corr := CorrelationFromContext(r.Context())
	reqID := corr.RequestID
	if reqID == "" {
		reqID = w.Header().Get("X-Request-Id")
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if reqID != "" {
		w.Header().Set("X-Request-Id", reqID)
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(GatewayError{
		Status:    status,
		Code:      code,
		Message:   redact.String(message),
		RequestID: reqID,
		TraceID:   corr.TraceID,
	})
}
