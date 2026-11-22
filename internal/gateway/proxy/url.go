package proxy

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path"
	"strings"
)

var (
	ErrBlockedDestination  = errors.New("upstream destination is blocked by security policy")
	ErrInvalidProxyRequest = errors.New("invalid proxy request URI")
)

// DestinationPolicy controls which upstream hosts and IP ranges the gateway may connect to.
type DestinationPolicy struct {
	AllowMockUpstreams bool
	AllowedMockHosts   map[string]struct{}
	AllowLoopback      bool // ONLY for local unit/integration tests using httptest.Server
}

// DefaultDestinationPolicy enforces strict SSRF defaults suitable for production.
func DefaultDestinationPolicy(allowMocks bool) DestinationPolicy {
	mocks := map[string]struct{}{}
	if allowMocks {
		mocks["mock-upstream"] = struct{}{}
		mocks["mock-upstream-1"] = struct{}{}
		mocks["mock-upstream-2"] = struct{}{}
	}
	return DestinationPolicy{
		AllowMockUpstreams: allowMocks,
		AllowedMockHosts:   mocks,
		AllowLoopback:      false,
	}
}

// ValidateTargetURL validates a configured upstream target URL against SSRF and URL hygiene rules.
func ValidateTargetURL(rawURL string, pol DestinationPolicy) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, fmt.Errorf("%w: invalid URL syntax", ErrBlockedDestination)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, fmt.Errorf("%w: scheme must be http or https", ErrBlockedDestination)
	}
	if u.Host == "" || u.Hostname() == "" {
		return nil, fmt.Errorf("%w: missing host", ErrBlockedDestination)
	}
	if u.User != nil {
		return nil, fmt.Errorf("%w: embedded URL credentials are forbidden", ErrBlockedDestination)
	}
	if u.Fragment != "" {
		return nil, fmt.Errorf("%w: URL fragments are forbidden", ErrBlockedDestination)
	}

	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if isBlockedMetadataHost(host) {
		return nil, fmt.Errorf("%w: cloud metadata host %q is forbidden", ErrBlockedDestination, host)
	}

	if ip, err := netip.ParseAddr(host); err == nil {
		if err := ValidateDialAddr(ip, host, pol); err != nil {
			return nil, err
		}
		return u, nil
	}

	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		if !pol.AllowLoopback {
			return nil, fmt.Errorf("%w: localhost destination is forbidden", ErrBlockedDestination)
		}
	}

	return u, nil
}

// ValidateDialIP enforces IP-level SSRF checks at connection time (preventing DNS rebinding).
func ValidateDialIP(ip net.IP, host string, pol DestinationPolicy) error {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return fmt.Errorf("%w: invalid resolved IP", ErrBlockedDestination)
	}
	return ValidateDialAddr(addr.Unmap(), host, pol)
}

// ValidateDialAddr checks a resolved netip.Addr against metadata, loopback, link-local, and private CIDRs.
func ValidateDialAddr(addr netip.Addr, host string, pol DestinationPolicy) error {
	addr = addr.Unmap()
	lowerHost := strings.ToLower(strings.TrimSuffix(host, "."))

	// Cloud metadata endpoints are ALWAYS blocked even in mock/test modes.
	if isMetadataAddr(addr) {
		return fmt.Errorf("%w: metadata IP %s is forbidden", ErrBlockedDestination, addr)
	}

	// Unspecified, multicast, and link-local addresses are ALWAYS blocked.
	if addr.IsUnspecified() || addr.IsMulticast() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() {
		return fmt.Errorf("%w: non-routable IP %s is forbidden", ErrBlockedDestination, addr)
	}

	if addr.IsLoopback() {
		if pol.AllowLoopback {
			return nil
		}
		return fmt.Errorf("%w: loopback IP %s is forbidden", ErrBlockedDestination, addr)
	}

	if addr.IsPrivate() {
		if pol.AllowMockUpstreams {
			if _, allowed := pol.AllowedMockHosts[lowerHost]; allowed {
				return nil
			}
		}
		return fmt.Errorf("%w: private network IP %s is forbidden for host %q", ErrBlockedDestination, addr, lowerHost)
	}

	return nil
}

// BuildUpstreamURL safely constructs the outbound upstream URL from an approved target and incoming request.
// Rejects absolute-URI open-proxy requests and path traversal attempts.
func BuildUpstreamURL(target *url.URL, req *http.Request, routePrefix string, stripPrefix bool) (*url.URL, error) {
	if target == nil || req == nil || req.URL == nil {
		return nil, ErrInvalidProxyRequest
	}

	// Reject explicit absolute-form proxy requests (e.g., GET http://evil.com/ HTTP/1.1).
	rawURI := strings.TrimSpace(req.RequestURI)
	lowerURI := strings.ToLower(rawURI)
	if strings.HasPrefix(lowerURI, "http://") || strings.HasPrefix(lowerURI, "https://") {
		return nil, fmt.Errorf("%w: absolute proxy URI is forbidden", ErrInvalidProxyRequest)
	}
	if req.URL.Scheme != "" || req.URL.Host != "" {
		// Standard net/http server requests have empty URL.Scheme and URL.Host;
		// non-empty values indicate absolute-form forward-proxy attempt unless httptest constructed it.
		if rawURI != "" && (strings.HasPrefix(lowerURI, "http://") || strings.HasPrefix(lowerURI, "https://")) {
			return nil, fmt.Errorf("%w: absolute proxy URI is forbidden", ErrInvalidProxyRequest)
		}
	}

	rawPath := req.URL.EscapedPath()
	if hasPathTraversal(req.URL.Path) || hasPathTraversal(rawPath) {
		return nil, fmt.Errorf("%w: path traversal sequence is forbidden", ErrInvalidProxyRequest)
	}

	reqPath := req.URL.Path
	if reqPath == "" {
		reqPath = "/"
	}

	if stripPrefix && routePrefix != "" && routePrefix != "/" {
		normPrefix := strings.TrimRight(routePrefix, "/")
		if reqPath == normPrefix {
			reqPath = "/"
		} else if strings.HasPrefix(reqPath, normPrefix+"/") {
			reqPath = strings.TrimPrefix(reqPath, normPrefix)
		}
	}

	joinedPath := joinURLPaths(target.Path, reqPath)
	out := &url.URL{
		Scheme:   target.Scheme,
		Host:     target.Host,
		Path:     joinedPath,
		RawQuery: req.URL.RawQuery,
	}
	return out, nil
}

func joinURLPaths(base, sub string) string {
	base = strings.TrimRight(base, "/")
	if !strings.HasPrefix(sub, "/") {
		sub = "/" + sub
	}
	if base == "" {
		return sub
	}
	return base + sub
}

func hasPathTraversal(p string) bool {
	if strings.Contains(p, "\\") || strings.Contains(p, "\x00") {
		return true
	}
	lower := strings.ToLower(p)
	if strings.Contains(lower, "%2e%2e") || strings.Contains(lower, "%2f..") || strings.Contains(lower, "..%2f") || strings.Contains(lower, "%5c") {
		return true
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return true
		}
	}
	cleaned := path.Clean("/" + p)
	return strings.HasPrefix(cleaned, "/..")
}

func isBlockedMetadataHost(host string) bool {
	switch host {
	case "metadata.google.internal", "metadata.goog", "metadata", "169.254.169.254", "169.254.170.2":
		return true
	}
	return false
}

func isMetadataAddr(addr netip.Addr) bool {
	awsGcpMeta := netip.MustParseAddr("169.254.169.254")
	ecsMeta := netip.MustParseAddr("169.254.170.2")
	awsV6Meta := netip.MustParseAddr("fd00:ec2::254")
	return addr == awsGcpMeta || addr == ecsMeta || addr == awsV6Meta
}
