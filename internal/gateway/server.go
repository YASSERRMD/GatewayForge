package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/YASSERRMD/GatewayForge/internal/shared/health"
)

// ServerConfig configures the gateway data-plane HTTP listener.
type ServerConfig struct {
	Addr              string
	ReadHeaderTimeout time.Duration
	IdleTimeout       time.Duration
	ShutdownTimeout   time.Duration
}

// DefaultServerConfig returns safe defaults for the gateway data plane.
func DefaultServerConfig() ServerConfig {
	addr := os.Getenv("GATEWAY_ADDR")
	if addr == "" {
		addr = ":8081"
	}
	shutdownSec := 15
	if raw := os.Getenv("GATEWAY_SHUTDOWN_TIMEOUT_SEC"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			shutdownSec = parsed
		}
	}
	return ServerConfig{
		Addr:              addr,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		ShutdownTimeout:   time.Duration(shutdownSec) * time.Second,
	}
}

// Server wraps the independent gateway data-plane HTTP server and health probes.
type Server struct {
	cfg        ServerConfig
	httpServer *http.Server
	health     *health.Registry
	mux        *http.ServeMux
}

// NewServer constructs a gateway data-plane server with independent health endpoints
// and a deny-by-default fallback handler (no open proxy).
func NewServer(cfg ServerConfig) *Server {
	if cfg.Addr == "" {
		cfg.Addr = ":8081"
	}
	if cfg.ReadHeaderTimeout <= 0 {
		cfg.ReadHeaderTimeout = 5 * time.Second
	}
	if cfg.ShutdownTimeout <= 0 {
		cfg.ShutdownTimeout = 15 * time.Second
	}

	reg := health.NewRegistry("gateway")
	mux := http.NewServeMux()
	mux.HandleFunc("/health/live", reg.LiveHandler())
	mux.HandleFunc("/health/ready", reg.ReadyHandler())
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"code":    "route_not_found",
			"message": "no configured route matches this request",
		})
	})

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           mux,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		IdleTimeout:       cfg.IdleTimeout,
	}

	return &Server{
		cfg:        cfg,
		httpServer: srv,
		health:     reg,
		mux:        mux,
	}
}

// Health returns the gateway readiness registry.
func (s *Server) Health() *health.Registry {
	return s.health
}

// Handler returns the root HTTP handler.
func (s *Server) Handler() http.Handler {
	return s.mux
}

// Serve serves requests on ln until ctx is canceled, then drains cleanly.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	errCh := make(chan error, 1)
	go func() {
		if err := s.httpServer.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		s.health.SetDraining(true)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), s.cfg.ShutdownTimeout)
		defer cancel()
		if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("gateway shutdown: %w", err)
		}
		return <-errCh
	}
}

// Run binds the configured TCP address and serves until ctx is canceled.
func (s *Server) Run(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.cfg.Addr)
	if err != nil {
		return fmt.Errorf("gateway listen %s: %w", s.cfg.Addr, err)
	}
	return s.Serve(ctx, ln)
}
