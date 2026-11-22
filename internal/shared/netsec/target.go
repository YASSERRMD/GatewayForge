package netsec

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"strings"
)

// ErrDisallowedTarget is returned when an upstream target URL violates destination security policy.
var ErrDisallowedTarget = errors.New("upstream target URL is disallowed by destination policy")

// TargetPolicy controls which destinations may be configured as upstream targets.
type TargetPolicy struct {
	AllowMockUpstreams bool
	AllowedMockHosts   map[string]struct{}
	AllowLoopback      bool
}

// DefaultTargetPolicy builds the standard environment destination policy.
func DefaultTargetPolicy(allowMocks bool) TargetPolicy {
	mocks := map[string]struct{}{
		"mock-upstream":   {},
		"mock-upstream-1": {},
		"mock-upstream-2": {},
	}
	return TargetPolicy{
		AllowMockUpstreams: allowMocks,
		AllowedMockHosts:   mocks,
		AllowLoopback:      false,
	}
}

// ValidateTargetURL checks URL syntax, scheme, credentials, and SSRF destination rules.
func ValidateTargetURL(raw string, pol TargetPolicy) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("%w: invalid URL syntax", ErrDisallowedTarget)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, fmt.Errorf("%w: scheme must be http or https", ErrDisallowedTarget)
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "" {
		return nil, fmt.Errorf("%w: host is required", ErrDisallowedTarget)
	}
	if u.User != nil {
		return nil, fmt.Errorf("%w: embedded credentials in URL are forbidden", ErrDisallowedTarget)
	}
	if u.Fragment != "" {
		return nil, fmt.Errorf("%w: URL fragments are forbidden", ErrDisallowedTarget)
	}
	if isMetadataHost(host) {
		return nil, fmt.Errorf("%w: cloud metadata host %q is forbidden", ErrDisallowedTarget, host)
	}

	if ip, err := netip.ParseAddr(host); err == nil {
		addr := ip.Unmap()
		if isMetadataAddr(addr) || addr.IsUnspecified() || addr.IsMulticast() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() {
			return nil, fmt.Errorf("%w: non-routable or metadata IP %s is forbidden", ErrDisallowedTarget, addr)
		}
		if addr.IsLoopback() && !pol.AllowLoopback {
			return nil, fmt.Errorf("%w: loopback IP %s is forbidden", ErrDisallowedTarget, addr)
		}
		if addr.IsPrivate() {
			return nil, fmt.Errorf("%w: raw private IP %s is forbidden", ErrDisallowedTarget, addr)
		}
		return u, nil
	}

	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		if !pol.AllowLoopback {
			return nil, fmt.Errorf("%w: localhost is forbidden", ErrDisallowedTarget)
		}
		return u, nil
	}

	if _, isMock := pol.AllowedMockHosts[host]; isMock && !pol.AllowMockUpstreams {
		return nil, fmt.Errorf("%w: mock upstream host %q is disabled in this environment", ErrDisallowedTarget, host)
	}

	return u, nil
}

func isMetadataHost(host string) bool {
	switch host {
	case "metadata.google.internal", "metadata.goog", "metadata", "169.254.169.254", "169.254.170.2":
		return true
	}
	return false
}

func isMetadataAddr(addr netip.Addr) bool {
	return addr == netip.MustParseAddr("169.254.169.254") ||
		addr == netip.MustParseAddr("169.254.170.2") ||
		addr == netip.MustParseAddr("fd00:ec2::254")
}
