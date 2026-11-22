package mockupstream

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	maxEchoBodyBytes  = 1 << 20 // 1 MiB
	maxDelayMillis    = 30000
	maxStreamBytes    = 8 << 20 // 8 MiB
	defaultInstanceID = "mock-upstream-1"
)

// EchoResponse is returned by /echo endpoints for deterministic proxy verification.
type EchoResponse struct {
	Instance string              `json:"instance"`
	Method   string              `json:"method"`
	Path     string              `json:"path"`
	RawQuery string              `json:"raw_query"`
	Host     string              `json:"host"`
	Headers  map[string][]string `json:"headers"`
	Body     string              `json:"body"`
	BodySize int                 `json:"body_size"`
}

// NewHandler constructs the deterministic mock upstream HTTP handler.
func NewHandler(instanceID string) http.Handler {
	if strings.TrimSpace(instanceID) == "" {
		instanceID = defaultInstanceID
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{
			"status":   "ok",
			"service":  "mock-upstream",
			"instance": instanceID,
		})
	})

	mux.HandleFunc("/status/", func(w http.ResponseWriter, r *http.Request) {
		raw := strings.TrimPrefix(r.URL.Path, "/status/")
		raw = strings.SplitN(raw, "/", 2)[0]
		code, err := strconv.Atoi(raw)
		if err != nil || code < 200 || code > 599 {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "status code must be an integer between 200 and 599",
			})
			return
		}
		if code == http.StatusNoContent {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeJSON(w, code, map[string]any{
			"instance": instanceID,
			"status":   code,
			"text":     http.StatusText(code),
		})
	})

	mux.HandleFunc("/delay/", func(w http.ResponseWriter, r *http.Request) {
		raw := strings.TrimPrefix(r.URL.Path, "/delay/")
		raw = strings.SplitN(raw, "/", 2)[0]
		ms, err := strconv.Atoi(raw)
		if err != nil || ms < 0 || ms > maxDelayMillis {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "delay milliseconds must be between 0 and 30000",
			})
			return
		}
		start := time.Now()
		timer := time.NewTimer(time.Duration(ms) * time.Millisecond)
		defer timer.Stop()
		select {
		case <-r.Context().Done():
			w.WriteHeader(499)
			return
		case <-timer.C:
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"instance":     instanceID,
			"requested_ms": ms,
			"elapsed_ms":   time.Since(start).Milliseconds(),
		})
	})

	mux.HandleFunc("/headers", func(w http.ResponseWriter, r *http.Request) {
		applied := make(map[string]string)
		for _, pair := range r.URL.Query()["set"] {
			k, v, ok := strings.Cut(pair, ":")
			k = strings.TrimSpace(k)
			v = strings.TrimSpace(v)
			if !ok || k == "" || strings.ContainsAny(k+v, "\r\n") {
				continue
			}
			w.Header().Add(k, v)
			applied[k] = v
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"instance": instanceID,
			"applied":  applied,
		})
	})

	mux.HandleFunc("/stream/", func(w http.ResponseWriter, r *http.Request) {
		raw := strings.TrimPrefix(r.URL.Path, "/stream/")
		size, err := strconv.Atoi(raw)
		if err != nil || size < 0 || size > maxStreamBytes {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "stream size must be between 0 and 8388608 bytes",
			})
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("X-Mock-Instance", instanceID)
		w.WriteHeader(http.StatusOK)
		chunk := bytes.Repeat([]byte("A"), 4096)
		remaining := size
		for remaining > 0 {
			n := len(chunk)
			if remaining < n {
				n = remaining
			}
			if _, err := w.Write(chunk[:n]); err != nil {
				return
			}
			remaining -= n
		}
	})

	echoHandler := func(w http.ResponseWriter, r *http.Request) {
		limited := io.LimitReader(r.Body, maxEchoBodyBytes+1)
		bodyBytes, err := io.ReadAll(limited)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to read body"})
			return
		}
		if len(bodyBytes) > maxEchoBodyBytes {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "echo body limit exceeded"})
			return
		}
		headers := make(map[string][]string, len(r.Header))
		keys := make([]string, 0, len(r.Header))
		for k := range r.Header {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			vals := append([]string(nil), r.Header[k]...)
			headers[k] = vals
		}
		w.Header().Set("X-Mock-Instance", instanceID)
		writeJSON(w, http.StatusOK, EchoResponse{
			Instance: instanceID,
			Method:   r.Method,
			Path:     r.URL.Path,
			RawQuery: r.URL.RawQuery,
			Host:     r.Host,
			Headers:  headers,
			Body:     string(bodyBytes),
			BodySize: len(bodyBytes),
		})
	}
	mux.HandleFunc("/echo", echoHandler)
	mux.HandleFunc("/echo/", echoHandler)
	mux.HandleFunc("/", echoHandler)

	return mux
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
