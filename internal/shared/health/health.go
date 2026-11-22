package health

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/YASSERRMD/GatewayForge/internal/shared/buildinfo"
)

// CheckFunc probes a dependency for readiness. Returning nil means healthy.
type CheckFunc func(ctx context.Context) error

// CheckResult captures an individual dependency probe result.
type CheckResult struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// Response is the structured health payload returned by /health/live and /health/ready.
type Response struct {
	Status    string                 `json:"status"`
	Service   string                 `json:"service"`
	Version   string                 `json:"version"`
	Timestamp string                 `json:"timestamp"`
	Checks    map[string]CheckResult `json:"checks,omitempty"`
}

// Registry manages named readiness checks and shutdown draining state.
type Registry struct {
	mu           sync.RWMutex
	service      string
	checks       map[string]CheckFunc
	draining     bool
	probeTimeout time.Duration
}

// NewRegistry creates a health registry for the given service.
func NewRegistry(service string) *Registry {
	return &Registry{
		service:      service,
		checks:       make(map[string]CheckFunc),
		probeTimeout: 2 * time.Second,
	}
}

// Register adds or replaces a named readiness probe.
func (r *Registry) Register(name string, fn CheckFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.checks[name] = fn
}

// SetDraining marks the service as draining so readiness returns 503 immediately.
func (r *Registry) SetDraining(draining bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.draining = draining
}

// LiveHandler serves GET /health/live.
func (r *Registry) LiveHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet && req.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{
				"error": "method_not_allowed",
			})
			return
		}
		info := buildinfo.ForService(r.service)
		writeJSON(w, http.StatusOK, Response{
			Status:    "ok",
			Service:   info.Service,
			Version:   info.Version,
			Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		})
	}
}

// ReadyHandler serves GET /health/ready by executing all registered probes.
func (r *Registry) ReadyHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet && req.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{
				"error": "method_not_allowed",
			})
			return
		}

		r.mu.RLock()
		draining := r.draining
		checksCopy := make(map[string]CheckFunc, len(r.checks))
		for k, v := range r.checks {
			checksCopy[k] = v
		}
		timeout := r.probeTimeout
		r.mu.RUnlock()

		info := buildinfo.ForService(r.service)
		resp := Response{
			Status:    "ready",
			Service:   info.Service,
			Version:   info.Version,
			Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
			Checks:    make(map[string]CheckResult, len(checksCopy)),
		}

		if draining {
			resp.Status = "draining"
			writeJSON(w, http.StatusServiceUnavailable, resp)
			return
		}

		allHealthy := true
		for name, fn := range checksCopy {
			ctx, cancel := context.WithTimeout(req.Context(), timeout)
			err := fn(ctx)
			cancel()
			if err != nil {
				allHealthy = false
				resp.Checks[name] = CheckResult{
					Status: "fail",
					Error:  err.Error(),
				}
			} else {
				resp.Checks[name] = CheckResult{Status: "ok"}
			}
		}

		if !allHealthy {
			resp.Status = "not_ready"
			writeJSON(w, http.StatusServiceUnavailable, resp)
			return
		}

		writeJSON(w, http.StatusOK, resp)
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
