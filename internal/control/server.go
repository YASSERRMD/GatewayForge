package control

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/YASSERRMD/GatewayForge/internal/shared/health"
)

// ServerConfig configures the control-plane HTTP server.
type ServerConfig struct {
	Addr              string
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	ShutdownTimeout   time.Duration
}

// DefaultServerConfig returns safe defaults for the control-plane HTTP server.
func DefaultServerConfig() ServerConfig {
	addr := os.Getenv("CONTROL_PLANE_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	shutdownSec := 10
	if raw := os.Getenv("CONTROL_PLANE_SHUTDOWN_TIMEOUT_SEC"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			shutdownSec = parsed
		}
	}
	return ServerConfig{
		Addr:              addr,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		ShutdownTimeout:   time.Duration(shutdownSec) * time.Second,
	}
}

// Server wraps the control-plane HTTP listener and health registry.
type Server struct {
	cfg        ServerConfig
	httpServer *http.Server
	health     *health.Registry
	mux        *http.ServeMux
}

// NewServer constructs a control-plane HTTP server with health endpoints registered.
func NewServer(cfg ServerConfig) *Server {
	if cfg.Addr == "" {
		cfg.Addr = ":8080"
	}
	if cfg.ReadHeaderTimeout <= 0 {
		cfg.ReadHeaderTimeout = 5 * time.Second
	}
	if cfg.ShutdownTimeout <= 0 {
		cfg.ShutdownTimeout = 10 * time.Second
	}

	reg := health.NewRegistry("control-plane")
	mux := http.NewServeMux()
	mux.HandleFunc("/health/live", reg.LiveHandler())
	mux.HandleFunc("/health/ready", reg.ReadyHandler())

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           mux,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
	}

	return &Server{
		cfg:        cfg,
		httpServer: srv,
		health:     reg,
		mux:        mux,
	}
}

// Health returns the server's readiness registry for registering dependency checks.
func (s *Server) Health() *health.Registry {
	return s.health
}

// Handler returns the root HTTP handler for testing or embedding.
func (s *Server) Handler() http.Handler {
	return s.mux
}

// Serve accepts incoming connections on ln and shuts down gracefully when ctx is canceled.
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
			return fmt.Errorf("control-plane shutdown: %w", err)
		}
		return <-errCh
	}
}

// Run binds the configured address and serves until ctx is canceled.
func (s *Server) Run(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.cfg.Addr)
	if err != nil {
		return fmt.Errorf("control-plane listen %s: %w", s.cfg.Addr, err)
	}
	return s.Serve(ctx, ln)
}
