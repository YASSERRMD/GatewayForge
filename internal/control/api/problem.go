package api

import (
	"context"
	"encoding/json"
	"net/http"
)

type ctxKey string

const requestIDCtxKey ctxKey = "request_id"

// FieldError describes a validation error on a specific input field.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Problem represents an RFC 9457 Problem Details error response.
type Problem struct {
	Type      string       `json:"type"`
	Title     string       `json:"title"`
	Status    int          `json:"status"`
	Detail    string       `json:"detail,omitempty"`
	Code      string       `json:"code"`
	RequestID string       `json:"request_id,omitempty"`
	Errors    []FieldError `json:"errors,omitempty"`
}

// WithRequestID stores reqID in ctx.
func WithRequestID(ctx context.Context, reqID string) context.Context {
	return context.WithValue(ctx, requestIDCtxKey, reqID)
}

// RequestIDFromContext extracts the correlation request ID from ctx.
func RequestIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(requestIDCtxKey).(string); ok {
		return v
	}
	return ""
}

// WriteJSON writes a JSON response with status code and cache-control defaults.
func WriteJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// WriteProblem serializes an RFC 9457 problem+json response including request_id.
func WriteProblem(w http.ResponseWriter, r *http.Request, status int, code, title, detail string, fieldErrs ...FieldError) {
	reqID := RequestIDFromContext(r.Context())
	if reqID == "" {
		reqID = w.Header().Get("X-Request-Id")
	}
	p := Problem{
		Type:      "https://gatewayforge.dev/problems/" + code,
		Title:     title,
		Status:    status,
		Detail:    detail,
		Code:      code,
		RequestID: reqID,
		Errors:    fieldErrs,
	}
	w.Header().Set("Content-Type", "application/problem+json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(p)
}
