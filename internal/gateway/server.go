package gateway

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/YASSERRMD/GatewayForge/internal/gateway/proxy"
	"github.com/YASSERRMD/GatewayForge/internal/gateway/table"
	"github.com/YASSERRMD/GatewayForge/internal/shared/health"
)

// ServerConfig configures the gateway data-plane HTTP listener.
type ServerConfig struct {
	Addr               string
	ReadHeaderTimeout  time.Duration
	IdleTimeout        time.Duration
	ShutdownTimeout    time.Duration
	AllowMockUpstreams bool
	AllowLoopback      bool
	TrustedProxyCIDRs  []string
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
		Addr:               addr,
		ReadHeaderTimeout:  5 * time.Second,
		IdleTimeout:        60 * time.Second,
		ShutdownTimeout:    time.Duration(shutdownSec) * time.Second,
		AllowMockUpstreams: os.Getenv("ALLOW_MOCK_UPSTREAMS") == "true",
	}
}

// Server wraps the independent gateway data-plane HTTP server, immutable routing table,
// SSRF-safe HTTP transport, proxy engine, and health probes.
type Server struct {
	cfg        ServerConfig
	httpServer *http.Server
	health     *health.Registry
	holder     *table.Holder
	transport  *http.Transport
	engine     *proxy.Engine
	mux        *http.ServeMux
}

// NewServer constructs a gateway data-plane server with independent health endpoints
// and a deny-by-default reverse-proxy engine.
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
	holder := table.NewHolder()
	pol := proxy.DefaultDestinationPolicy(cfg.AllowMockUpstreams)
	pol.AllowLoopback = cfg.AllowLoopback
	tr := proxy.NewSafeTransport(proxy.DefaultTransportConfig(), pol)
	eng := proxy.NewEngine(holder, tr, pol)
	if len(cfg.TrustedProxyCIDRs) > 0 {
		if trusted, err := proxy.ParseTrustedProxies(cfg.TrustedProxyCIDRs); err == nil {
			eng.TrustedProxies = trusted
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health/live", reg.LiveHandler())
	mux.HandleFunc("/health/ready", reg.ReadyHandler())
	mux.Handle("/", eng)

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
		holder:     holder,
		transport:  tr,
		engine:     eng,
		mux:        mux,
	}
}

// Health returns the gateway readiness registry.
func (s *Server) Health() *health.Registry {
	return s.health
}

// TableHolder returns the atomic routing table holder for snapshot updates.
func (s *Server) TableHolder() *table.Holder {
	return s.holder
}

// Engine returns the reverse-proxy engine.
func (s *Server) Engine() *proxy.Engine {
	return s.engine
}

// Handler returns the root HTTP handler.
func (s *Server) Handler() http.Handler {
	return s.mux
}

// Serve serves requests on ln until ctx is canceled, then drains in-flight requests
// and closes outbound idle connections cleanly.
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
		s.transport.CloseIdleConnections()
		return err
	case <-ctx.Done():
		s.health.SetDraining(true)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), s.cfg.ShutdownTimeout)
		defer cancel()
		err := s.httpServer.Shutdown(shutdownCtx)
		s.transport.CloseIdleConnections()
		if err != nil {
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
