package api

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/YASSERRMD/GatewayForge/internal/shared/health"
)

// RouterOptions configures the control-plane Chi HTTP router.
type RouterOptions struct {
	Health             *health.Registry
	Logger             *slog.Logger
	AllowedCORSOrigins []string
	MountV1            func(r chi.Router)
}

// NewRouter constructs the versioned control-plane Chi router with explicit middleware ordering
// and consistent RFC 9457 error responses for 404, 405, and panics.
func NewRouter(opts RouterOptions) *chi.Mux {
	if opts.Health == nil {
		opts.Health = health.NewRegistry("control-plane")
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	r := chi.NewRouter()
	r.Use(requestIDMiddleware)
	r.Use(recoverMiddleware(opts.Logger))
	r.Use(securityHeadersMiddleware)
	r.Use(corsMiddleware(opts.AllowedCORSOrigins))
	r.Use(accessLogMiddleware(opts.Logger))

	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		WriteProblem(w, req, http.StatusNotFound, "not_found", "Resource Not Found", "The requested endpoint does not exist.")
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, req *http.Request) {
		WriteProblem(w, req, http.StatusMethodNotAllowed, "method_not_allowed", "Method Not Allowed", "The HTTP method is not supported on this endpoint.")
	})

	r.Get("/health/live", opts.Health.LiveHandler())
	r.Head("/health/live", opts.Health.LiveHandler())
	r.Get("/health/ready", opts.Health.ReadyHandler())
	r.Head("/health/ready", opts.Health.ReadyHandler())

	r.Route("/api/v1", func(v1 chi.Router) {
		v1.Get("/meta", func(w http.ResponseWriter, req *http.Request) {
			WriteJSON(w, http.StatusOK, map[string]any{
				"api_version": "v1",
				"service":     "control-plane",
				"request_id":  RequestIDFromContext(req.Context()),
			})
		})
		if opts.MountV1 != nil {
			opts.MountV1(v1)
		}
	})

	return r
}

func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := sanitizeRequestID(r.Header.Get("X-Request-Id"))
		if reqID == "" {
			reqID = newRequestID()
		}
		w.Header().Set("X-Request-Id", reqID)
		next.ServeHTTP(w, r.WithContext(WithRequestID(r.Context(), reqID)))
	})
}

func sanitizeRequestID(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if len(trimmed) < 4 || len(trimmed) > 128 {
		return ""
	}
	for _, ch := range trimmed {
		if !isSafeRequestIDChar(ch) {
			return ""
		}
	}
	return trimmed
}

func isSafeRequestIDChar(ch rune) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_' || ch == '.'
}

func newRequestID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return "req_" + hex.EncodeToString(b[:])
}

func recoverMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					logger.Error("control-plane panic recovered",
						"request_id", RequestIDFromContext(r.Context()),
						"path", r.URL.Path,
					)
					WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Internal Server Error", "An unexpected internal error occurred.")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		next.ServeHTTP(w, r)
	})
}

func corsMiddleware(allowed []string) func(http.Handler) http.Handler {
	allowSet := make(map[string]struct{}, len(allowed))
	for _, origin := range allowed {
		if origin != "" && origin != "*" {
			allowSet[origin] = struct{}{}
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" {
				if _, ok := allowSet[origin]; ok {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					w.Header().Set("Vary", "Origin")
					w.Header().Set("Access-Control-Allow-Credentials", "true")
					w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
					w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-CSRF-Token, X-Request-Id, If-Match, Idempotency-Key")
					if r.Method == http.MethodOptions {
						w.WriteHeader(http.StatusNoContent)
						return
					}
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (sr *statusRecorder) WriteHeader(code int) {
	sr.status = code
	sr.ResponseWriter.WriteHeader(code)
}

func accessLogMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			logger.Info("control-plane request",
				"request_id", RequestIDFromContext(r.Context()),
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"duration_ms", time.Since(start).Milliseconds(),
			)
		})
	}
}
