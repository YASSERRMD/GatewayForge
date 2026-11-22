package redact

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
)

const RedactedValue = "[REDACTED]"

var sensitiveHeaderKeys = map[string]struct{}{
	"authorization":       {},
	"proxy-authorization": {},
	"cookie":              {},
	"set-cookie":          {},
	"x-api-key":           {},
	"x-csrf-token":        {},
	"x-gateway-token":     {},
	"x-auth-token":        {},
}

var sensitiveFieldFragments = []string{
	"password",
	"secret",
	"token",
	"api_key",
	"apikey",
	"authorization",
	"cookie",
	"credential",
	"private_key",
	"session",
}

var (
	// Matches GatewayForge full API keys (gwf_<prefix>.<secret> or gwf_<prefix>_<secret>) while preserving safe prefix-only values.
	apiKeyPattern = regexp.MustCompile(`\bgwf_[A-Za-z0-9]{8,}[._][A-Za-z0-9_-]{12,}\b`)
	// Matches compact JWS / JWT tokens (header.payload.signature)
	jwtPattern = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{6,}\.[A-Za-z0-9_-]{6,}\.[A-Za-z0-9_-]{6,}\b`)
	// Matches Bearer tokens in prose
	bearerPattern = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]{8,}`)
	// Matches Basic auth credentials
	basicPattern = regexp.MustCompile(`(?i)\bBasic\s+[A-Za-z0-9+/=]{8,}`)
)

// IsSensitiveHeader reports whether a header name carries credentials or session secrets.
func IsSensitiveHeader(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if _, ok := sensitiveHeaderKeys[lower]; ok {
		return true
	}
	return strings.Contains(lower, "secret") || strings.Contains(lower, "token") || strings.Contains(lower, "api-key")
}

// IsSensitiveKey reports whether a structured log/audit field name should always be masked.
func IsSensitiveKey(key string) bool {
	lower := strings.ToLower(strings.TrimSpace(key))
	if lower == "request_id" || lower == "trace_id" || lower == "key_prefix" || lower == "key_id" || lower == "token_type" {
		return false
	}
	for _, frag := range sensitiveFieldFragments {
		if strings.Contains(lower, frag) {
			return true
		}
	}
	return false
}

// Headers returns a sanitized shallow copy of h with sensitive header values replaced by [REDACTED].
func Headers(h http.Header) map[string][]string {
	out := make(map[string][]string, len(h))
	for k, vals := range h {
		if IsSensitiveHeader(k) {
			out[k] = []string{RedactedValue}
			continue
		}
		clean := make([]string, len(vals))
		for i, v := range vals {
			clean[i] = String(v)
		}
		out[k] = clean
	}
	return out
}

// String scrubs inline API keys, JWTs, Bearer, and Basic credentials from arbitrary text.
func String(s string) string {
	if s == "" {
		return ""
	}
	s = apiKeyPattern.ReplaceAllString(s, RedactedValue)
	s = jwtPattern.ReplaceAllString(s, RedactedValue)
	s = bearerPattern.ReplaceAllString(s, "Bearer "+RedactedValue)
	s = basicPattern.ReplaceAllString(s, "Basic "+RedactedValue)
	return s
}

// Map recursively redacts sensitive keys and credential patterns in a JSON-like map.
func Map(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		if IsSensitiveKey(k) {
			out[k] = RedactedValue
			continue
		}
		out[k] = sanitizeValue(v)
	}
	return out
}

func sanitizeValue(v any) any {
	switch tv := v.(type) {
	case string:
		return String(tv)
	case map[string]any:
		return Map(tv)
	case []any:
		cp := make([]any, len(tv))
		for i, item := range tv {
			cp[i] = sanitizeValue(item)
		}
		return cp
	default:
		return v
	}
}

// FindLeaks scans raw log/trace output for any occurrence of known secret substrings or raw credential patterns.
// It returns human-readable leak descriptions (never echoing the full secret).
func FindLeaks(output string, forbiddenSecrets ...string) []string {
	var leaks []string
	for i, secret := range forbiddenSecrets {
		if secret != "" && strings.Contains(output, secret) {
			leaks = append(leaks, "matched forbidden secret #"+itoa(i+1))
		}
	}
	if apiKeyPattern.MatchString(output) {
		leaks = append(leaks, "raw gwf_ API key pattern detected")
	}
	if jwtPattern.MatchString(output) {
		leaks = append(leaks, "raw JWT token pattern detected")
	}
	if matches := bearerPattern.FindAllString(output, -1); len(matches) > 0 {
		for _, m := range matches {
			if !strings.Contains(m, RedactedValue) {
				leaks = append(leaks, "raw Bearer credential detected")
				break
			}
		}
	}
	return leaks
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// NewJSONLogger returns a structured JSON slog.Logger wrapped with automatic secret/header redaction.
func NewJSONLogger(w io.Writer, opts *slog.HandlerOptions) *slog.Logger {
	base := slog.NewJSONHandler(w, opts)
	return slog.New(&SafeHandler{inner: base})
}

// SafeHandler wraps any slog.Handler and redacts sensitive keys and inline credential patterns.
type SafeHandler struct {
	inner slog.Handler
}

func (h *SafeHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *SafeHandler) Handle(ctx context.Context, r slog.Record) error {
	clean := slog.NewRecord(r.Time, r.Level, String(r.Message), r.PC)
	r.Attrs(func(a slog.Attr) bool {
		clean.AddAttrs(redactAttr(a))
		return true
	})
	return h.inner.Handle(ctx, clean)
}

func (h *SafeHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clean := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		clean[i] = redactAttr(a)
	}
	return &SafeHandler{inner: h.inner.WithAttrs(clean)}
}

func (h *SafeHandler) WithGroup(name string) slog.Handler {
	return &SafeHandler{inner: h.inner.WithGroup(name)}
}

func redactAttr(a slog.Attr) slog.Attr {
	if IsSensitiveKey(a.Key) {
		return slog.String(a.Key, RedactedValue)
	}
	switch a.Value.Kind() {
	case slog.KindString:
		return slog.String(a.Key, String(a.Value.String()))
	case slog.KindGroup:
		grp := a.Value.Group()
		out := make([]any, len(grp))
		for i, ga := range grp {
			out[i] = redactAttr(ga)
		}
		return slog.Group(a.Key, out...)
	case slog.KindAny:
		switch tv := a.Value.Any().(type) {
		case http.Header:
			return slog.Any(a.Key, Headers(tv))
		case map[string]any:
			return slog.Any(a.Key, Map(tv))
		case error:
			if tv != nil {
				return slog.String(a.Key, String(tv.Error()))
			}
		}
	}
	return a
}
