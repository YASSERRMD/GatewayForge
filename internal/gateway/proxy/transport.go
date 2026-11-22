package proxy

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/YASSERRMD/GatewayForge/internal/gateway/resilience"
)

// TransportConfig tunes outbound connection pooling and upstream dial/TLS timeouts.
type TransportConfig struct {
	DialTimeout           time.Duration
	KeepAlive             time.Duration
	MaxIdleConns          int
	MaxIdleConnsPerHost   int
	MaxConnsPerHost       int
	IdleConnTimeout       time.Duration
	TLSHandshakeTimeout   time.Duration
	ResponseHeaderTimeout time.Duration
	Resolver              *net.Resolver
}

// DefaultTransportConfig returns production-tuned pooling and timeout values.
func DefaultTransportConfig() TransportConfig {
	return TransportConfig{
		DialTimeout:           3 * time.Second,
		KeepAlive:             30 * time.Second,
		MaxIdleConns:          256,
		MaxIdleConnsPerHost:   64,
		MaxConnsPerHost:       256,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}
}

// NewSafeTransport constructs an *http.Transport with bounded pooling and a connect-time
// SSRF-validating DialContext that resolves DNS and dials the validated IP directly.
func NewSafeTransport(cfg TransportConfig, pol DestinationPolicy) *http.Transport {
	def := DefaultTransportConfig()
	if cfg.DialTimeout <= 0 {
		cfg.DialTimeout = def.DialTimeout
	}
	if cfg.KeepAlive <= 0 {
		cfg.KeepAlive = def.KeepAlive
	}
	if cfg.MaxIdleConns <= 0 {
		cfg.MaxIdleConns = def.MaxIdleConns
	}
	if cfg.MaxIdleConnsPerHost <= 0 {
		cfg.MaxIdleConnsPerHost = def.MaxIdleConnsPerHost
	}
	if cfg.MaxConnsPerHost <= 0 {
		cfg.MaxConnsPerHost = def.MaxConnsPerHost
	}
	if cfg.IdleConnTimeout <= 0 {
		cfg.IdleConnTimeout = def.IdleConnTimeout
	}
	if cfg.TLSHandshakeTimeout <= 0 {
		cfg.TLSHandshakeTimeout = def.TLSHandshakeTimeout
	}
	if cfg.ResponseHeaderTimeout <= 0 {
		cfg.ResponseHeaderTimeout = def.ResponseHeaderTimeout
	}
	resolver := cfg.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}

	dialer := &net.Dialer{
		Timeout:   cfg.DialTimeout,
		KeepAlive: cfg.KeepAlive,
	}

	return &http.Transport{
		Proxy:                 nil, // NEVER follow ambient HTTP_PROXY env vars automatically
		MaxIdleConns:          cfg.MaxIdleConns,
		MaxIdleConnsPerHost:   cfg.MaxIdleConnsPerHost,
		MaxConnsPerHost:       cfg.MaxConnsPerHost,
		IdleConnTimeout:       cfg.IdleConnTimeout,
		TLSHandshakeTimeout:   cfg.TLSHandshakeTimeout,
		ResponseHeaderTimeout: cfg.ResponseHeaderTimeout,
		ExpectContinueTimeout: 1 * time.Second,
		ForceAttemptHTTP2:     true,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if ct := resilience.ConnectTimeoutFromContext(ctx); ct > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, ct)
				defer cancel()
			}
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, fmt.Errorf("%w: malformed dial address", ErrBlockedDestination)
			}

			// Fast path if host is already a literal IP.
			if ip := net.ParseIP(host); ip != nil {
				if err := ValidateDialIP(ip, host, pol); err != nil {
					return nil, err
				}
				return dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			}

			// Resolve host and validate ALL returned IPs before dialing the first validated IP directly.
			ips, err := resolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, fmt.Errorf("resolve upstream host %q: %w", host, err)
			}
			if len(ips) == 0 {
				return nil, fmt.Errorf("%w: no DNS records for %q", ErrBlockedDestination, host)
			}
			for _, ipAddr := range ips {
				if err := ValidateDialIP(ipAddr.IP, host, pol); err != nil {
					return nil, err
				}
			}

			var lastErr error
			for _, ipAddr := range ips {
				conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(ipAddr.IP.String(), port))
				if dialErr == nil {
					return conn, nil
				}
				lastErr = dialErr
			}
			return nil, lastErr
		},
	}
}
