package api

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/YASSERRMD/GatewayForge/internal/control/auth"
	"github.com/YASSERRMD/GatewayForge/internal/control/rbac"
)

const (
	maxConsoleRequestBodyBytes  = 64 << 10  // 64 KiB
	maxConsoleResponseBodyBytes = 256 << 10 // 256 KiB
	maxConsoleHeaders           = 32
)

var (
	consoleHostPattern = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9.-]{0,251}[a-zA-Z0-9])?$`)
	blockedConsoleHdrs = map[string]struct{}{
		"host":                      {},
		"connection":                {},
		"keep-alive":                {},
		"proxy-authenticate":        {},
		"proxy-authorization":       {},
		"proxy-connection":          {},
		"te":                        {},
		"trailer":                   {},
		"transfer-encoding":         {},
		"upgrade":                   {},
		"content-length":            {},
		"x-forwarded-for":           {},
		"x-forwarded-host":          {},
		"x-forwarded-proto":         {},
		"x-real-ip":                 {},
		"forwarded":                 {},
		"x-gatewayforge-sync-token": {},
		"x-csrf-token":              {},
	}
)

// ConsoleHandler executes constrained developer test requests strictly through the configured
// GatewayForge data plane (never arbitrary user-supplied target URLs).
type ConsoleHandler struct {
	Sessions       *auth.SessionManager
	Audit          *AuditRecorder
	GatewayBaseURL string
	HTTPClient     *http.Client
}

// MountConsoleRoutes registers /orgs/{orgId}/console/execute under v1.
func (h *ConsoleHandler) MountConsoleRoutes(v1 chi.Router) {
	v1.With(
		RequireAuth(h.Sessions),
		RequireOrgContext(h.Sessions),
		AuditMutationsMiddleware(h.Audit),
		RequirePermission(rbac.PermConsoleUse),
	).Post("/orgs/{orgId}/console/execute", h.handleExecute)
}

type consoleExecuteRequest struct {
	Method  string            `json:"method"`
	Host    string            `json:"host"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

type consoleExecuteResponse struct {
	Status        int                 `json:"status"`
	DurationMS    int64               `json:"duration_ms"`
	Headers       map[string][]string `json:"headers"`
	Body          string              `json:"body"`
	BodyTruncated bool                `json:"body_truncated"`
	BodyBytes     int                 `json:"body_bytes"`
	IsBinary      bool                `json:"is_binary"`
	ContentType   string              `json:"content_type"`
	GatewayTarget string              `json:"gateway_target"`
}

func (h *ConsoleHandler) handleExecute(w http.ResponseWriter, r *http.Request) {
	var in consoleExecuteRequest
	if !DecodeStrictJSON(w, r, 128<<10, &in) {
		return
	}

	method := strings.ToUpper(strings.TrimSpace(in.Method))
	if _, ok := allowedRouteMethods[method]; !ok {
		WriteProblem(w, r, http.StatusUnprocessableEntity, "invalid_method", "Invalid HTTP Method", "Method must be one of GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS.")
		return
	}

	host := strings.ToLower(strings.TrimSpace(in.Host))
	if !consoleHostPattern.MatchString(host) {
		WriteProblem(w, r, http.StatusUnprocessableEntity, "invalid_host", "Invalid Route Host", "Host must be a valid route hostname without scheme, port, or path.")
		return
	}

	reqPath := strings.TrimSpace(in.Path)
	if !isSafeConsolePath(reqPath) {
		WriteProblem(w, r, http.StatusUnprocessableEntity, "invalid_path", "Invalid Request Path", "Path must be a relative origin-form path starting with '/' and cannot contain scheme, traversal, or control characters.")
		return
	}

	if len(in.Body) > maxConsoleRequestBodyBytes {
		WriteProblem(w, r, http.StatusRequestEntityTooLarge, "body_too_large", "Body Too Large", "Console request body cannot exceed 64 KiB.")
		return
	}

	if len(in.Headers) > maxConsoleHeaders {
		WriteProblem(w, r, http.StatusUnprocessableEntity, "too_many_headers", "Too Many Headers", "Console requests may include at most 32 headers.")
		return
	}

	baseRaw := strings.TrimSpace(h.GatewayBaseURL)
	if baseRaw == "" {
		baseRaw = "http://127.0.0.1:18081"
	}
	baseURL, err := url.Parse(baseRaw)
	if err != nil || (baseURL.Scheme != "http" && baseURL.Scheme != "https") || baseURL.Host == "" {
		WriteProblem(w, r, http.StatusServiceUnavailable, "console_misconfigured", "Console Unavailable", "Gateway console transport is not configured.")
		return
	}

	// Build target URL strictly anchored to the server-configured gateway base URL.
	parsedRel, err := url.Parse(reqPath)
	if err != nil || parsedRel.IsAbs() || parsedRel.Host != "" || parsedRel.Opaque != "" {
		WriteProblem(w, r, http.StatusUnprocessableEntity, "invalid_path", "Invalid Request Path", "Path must be a relative path starting with '/'.")
		return
	}
	targetURL := &url.URL{
		Scheme:   baseURL.Scheme,
		Host:     baseURL.Host,
		Path:     parsedRel.Path,
		RawPath:  parsedRel.RawPath,
		RawQuery: parsedRel.RawQuery,
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	var bodyReader io.Reader
	if method != http.MethodGet && method != http.MethodHead && in.Body != "" {
		bodyReader = bytes.NewBufferString(in.Body)
	}
	outReq, err := http.NewRequestWithContext(ctx, method, targetURL.String(), bodyReader)
	if err != nil {
		WriteProblem(w, r, http.StatusBadRequest, "invalid_request", "Invalid Request", "Could not construct gateway request.")
		return
	}
	// Route through GatewayForge using the selected route's virtual Host header.
	outReq.Host = host

	for k, v := range in.Headers {
		name := strings.TrimSpace(k)
		if name == "" || strings.ContainsAny(name, " \t\r\n:") || strings.ContainsAny(v, "\r\n") {
			continue
		}
		if _, blocked := blockedConsoleHdrs[strings.ToLower(name)]; blocked {
			continue
		}
		outReq.Header.Set(name, v)
	}
	outReq.Header.Set("X-GatewayForge-Console", "1")

	client := h.HTTPClient
	if client == nil {
		client = &http.Client{
			Timeout: 15 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}

	start := time.Now()
	resp, err := client.Do(outReq)
	durationMS := time.Since(start).Milliseconds()
	if err != nil {
		WriteProblem(w, r, http.StatusBadGateway, "gateway_unreachable", "Gateway Unreachable", "Could not reach the configured GatewayForge data plane.")
		return
	}
	defer func() { _ = resp.Body.Close() }()

	limited := io.LimitReader(resp.Body, maxConsoleResponseBodyBytes+1)
	rawBody, err := io.ReadAll(limited)
	if err != nil {
		WriteProblem(w, r, http.StatusBadGateway, "gateway_read_failed", "Gateway Read Error", "Could not read gateway response body.")
		return
	}

	truncated := false
	if len(rawBody) > maxConsoleResponseBodyBytes {
		truncated = true
		rawBody = rawBody[:maxConsoleResponseBodyBytes]
	}

	ct := resp.Header.Get("Content-Type")
	isBinary := isBinaryPayload(ct, rawBody)
	bodyOut := ""
	if !isBinary {
		bodyOut = string(rawBody)
	}

	respHeaders := make(map[string][]string, len(resp.Header))
	for k, vals := range resp.Header {
		copied := make([]string, len(vals))
		copy(copied, vals)
		respHeaders[k] = copied
	}

	WriteJSON(w, http.StatusOK, consoleExecuteResponse{
		Status:        resp.StatusCode,
		DurationMS:    durationMS,
		Headers:       respHeaders,
		Body:          bodyOut,
		BodyTruncated: truncated,
		BodyBytes:     len(rawBody),
		IsBinary:      isBinary,
		ContentType:   ct,
		GatewayTarget: baseURL.Scheme + "://" + baseURL.Host,
	})
}

func isSafeConsolePath(p string) bool {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") {
		return false
	}
	if strings.ContainsAny(p, "\r\n\x00\\") {
		return false
	}
	lower := strings.ToLower(p)
	if strings.Contains(lower, "://") || strings.Contains(p, "..") {
		return false
	}
	return true
}

func isBinaryPayload(contentType string, data []byte) bool {
	if len(data) == 0 {
		return false
	}
	ct := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	if strings.HasPrefix(ct, "image/") ||
		strings.HasPrefix(ct, "audio/") ||
		strings.HasPrefix(ct, "video/") ||
		ct == "application/octet-stream" ||
		ct == "application/pdf" ||
		ct == "application/zip" ||
		ct == "application/gzip" {
		return true
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return true
	}
	return !utf8.Valid(data)
}
