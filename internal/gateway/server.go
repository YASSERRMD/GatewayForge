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
	"sync"
	"time"

	gwauth "github.com/YASSERRMD/GatewayForge/internal/gateway/auth"
	gwmetrics "github.com/YASSERRMD/GatewayForge/internal/gateway/metrics"
	"github.com/YASSERRMD/GatewayForge/internal/gateway/proxy"
	"github.com/YASSERRMD/GatewayForge/internal/gateway/ratelimit"
	"github.com/YASSERRMD/GatewayForge/internal/gateway/resilience"
	syncer "github.com/YASSERRMD/GatewayForge/internal/gateway/sync"
	"github.com/YASSERRMD/GatewayForge/internal/gateway/table"
	"github.com/YASSERRMD/GatewayForge/internal/shared/health"
	"github.com/YASSERRMD/GatewayForge/internal/shared/netsec"
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
	cfg           ServerConfig
	httpServer    *http.Server
	health        *health.Registry
	holder        *table.Holder
	transport     *http.Transport
	engine        *proxy.Engine
	rlMetrics     *ratelimit.Telemetry
	promMetrics   *gwmetrics.Registry
	healthTracker *resilience.HealthTracker
	mux           *http.ServeMux

	outcomeMu sync.RWMutex
	onOutcome func(proxy.RequestOutcome)

	statusMu sync.RWMutex
	statusFn func() syncer.SyncStatus
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
	jwksPol := netsec.DefaultTargetPolicy(cfg.AllowMockUpstreams)
	jwksPol.AllowLoopback = cfg.AllowLoopback
	jwksCache := gwauth.NewJWKSCache(tr, jwksPol, 5*time.Minute)
	eng.Auth = &gwauth.Enforcer{
		APIKeys: gwauth.NewAPIKeyAuthenticator(nil),
		JWT:     gwauth.NewJWTVerifier(jwksCache),
	}
	ht := resilience.NewHealthTracker(resilience.DefaultActiveProbeConfig(), tr, nil)
	eng.Health = ht
	if len(cfg.TrustedProxyCIDRs) > 0 {
		if trusted, err := proxy.ParseTrustedProxies(cfg.TrustedProxyCIDRs); err == nil {
			eng.TrustedProxies = trusted
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health/live", reg.LiveHandler())
	mux.HandleFunc("/health/ready", reg.ReadyHandler())

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           mux,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		IdleTimeout:       cfg.IdleTimeout,
	}

	promReg := gwmetrics.NewRegistry(gwmetrics.DefaultMaxTrackedRoutes, gwmetrics.DefaultMaxTrackedServices)
	s := &Server{
		cfg:           cfg,
		httpServer:    srv,
		health:        reg,
		holder:        holder,
		transport:     tr,
		engine:        eng,
		rlMetrics:     ratelimit.NewTelemetry(ratelimit.DefaultMaxMetricRoutes),
		promMetrics:   promReg,
		healthTracker: ht,
		mux:           mux,
	}
	eng.OnOutcome = func(out proxy.RequestOutcome) {
		promReg.ObserveRequest(out.RouteID, out.ServiceID, out.Method, out.Status, out.Outcome, out.Duration)
		s.outcomeMu.RLock()
		cb := s.onOutcome
		s.outcomeMu.RUnlock()
		if cb != nil {
			cb(out)
		}
	}
	mux.HandleFunc("/health/config", s.handleConfigStatus)
	mux.HandleFunc("/health/ratelimit", s.handleRateLimitStatus)
	mux.HandleFunc("/health/upstreams", s.handleUpstreamHealth)
	mux.HandleFunc("/metrics", s.handlePrometheusMetrics)
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		promReg.IncInFlight()
		defer promReg.DecInFlight()
		eng.ServeHTTP(w, r)
	}))
	return s
}

// AttachSyncStatus binds a read-only sync diagnostics provider for /health/config.
func (s *Server) AttachSyncStatus(fn func() syncer.SyncStatus) {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	s.statusFn = fn
}

// AttachRateLimiter binds a distributed or local rate-limit/quota enforcer to the proxy engine
// and wires its bounded-cardinality telemetry recorder.
func (s *Server) AttachRateLimiter(enforcer *ratelimit.Enforcer) {
	if enforcer != nil && enforcer.OnEvent == nil {
		enforcer.OnEvent = s.rlMetrics.Record
	}
	s.engine.RateLimit = enforcer
}

// RateLimitTelemetry returns the bounded-cardinality limiter telemetry collector.
func (s *Server) RateLimitTelemetry() *ratelimit.Telemetry {
	return s.rlMetrics
}

func (s *Server) handleRateLimitStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Query().Get("format") == "prometheus" {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(s.rlMetrics.FormatPrometheus()))
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(s.rlMetrics.Snapshot())
}

// Metrics returns the bounded-cardinality Prometheus metrics registry.
func (s *Server) Metrics() *gwmetrics.Registry {
	return s.promMetrics
}

// SetOutcomeHook registers an additional hook invoked after every gateway request completes.
func (s *Server) SetOutcomeHook(fn func(proxy.RequestOutcome)) {
	s.outcomeMu.Lock()
	defer s.outcomeMu.Unlock()
	s.onOutcome = fn
}

func (s *Server) handlePrometheusMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if tbl := s.holder.Current(); tbl != nil {
		s.promMetrics.SetActiveConfigVersion(tbl.Version)
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(s.promMetrics.FormatPrometheus(s.rlMetrics.FormatPrometheus())))
}

func (s *Server) handleUpstreamHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	// Keep active health tracker synchronized with latest snapshot pools before reporting.
	if tbl := s.holder.Current(); tbl != nil {
		s.healthTracker.SyncPools(tbl.Pools)
	}
	pools := s.healthTracker.DiagnosticsSnapshot(true)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"pools": pools,
		"total": len(pools),
	})
}

func (s *Server) handleConfigStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	s.statusMu.RLock()
	fn := s.statusFn
	s.statusMu.RUnlock()

	var st syncer.SyncStatus
	if fn != nil {
		st = fn()
	} else {
		cur := s.holder.Current()
		st = syncer.SyncStatus{
			ActiveVersion: cur.Version,
			Checksum:      cur.Checksum,
			LastError:     s.holder.LastError(),
		}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(st)
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

// HealthTracker returns the active/passive upstream health tracker.
func (s *Server) HealthTracker() *resilience.HealthTracker {
	return s.healthTracker
}

// Serve serves requests on ln until ctx is canceled, then drains in-flight requests
// within ShutdownTimeout and force-closes any straggler connections cleanly.
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
		if err != nil {
			// Bounded grace exceeded: force-close remaining connections so shutdown never hangs.
			_ = s.httpServer.Close()
		}
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
