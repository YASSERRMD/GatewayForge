package proxy

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// HopByHopHeaders lists RFC 9110 connection-scoped headers that must never be forwarded across proxy hops.
var HopByHopHeaders = []string{
	"Connection",
	"Proxy-Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Te",
	"Trailer",
	"Transfer-Encoding",
	"Upgrade",
}

// TrustedProxyConfig defines peer CIDRs allowed to supply incoming X-Forwarded-* headers.
type TrustedProxyConfig struct {
	Prefixes []netip.Prefix
}

// ParseTrustedProxies parses CIDR or bare IP strings into a TrustedProxyConfig.
func ParseTrustedProxies(cidrs []string) (TrustedProxyConfig, error) {
	prefixes := make([]netip.Prefix, 0, len(cidrs))
	for _, raw := range cidrs {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		if strings.Contains(trimmed, "/") {
			p, err := netip.ParsePrefix(trimmed)
			if err != nil {
				return TrustedProxyConfig{}, fmt.Errorf("invalid trusted proxy CIDR %q: %w", trimmed, err)
			}
			prefixes = append(prefixes, p)
			continue
		}
		addr, err := netip.ParseAddr(trimmed)
		if err != nil {
			return TrustedProxyConfig{}, fmt.Errorf("invalid trusted proxy IP %q: %w", trimmed, err)
		}
		prefixes = append(prefixes, netip.PrefixFrom(addr.Unmap(), addr.Unmap().BitLen()))
	}
	return TrustedProxyConfig{Prefixes: prefixes}, nil
}

// IsTrusted reports whether peerIP belongs to any configured trusted proxy CIDR.
func (c TrustedProxyConfig) IsTrusted(peerIP netip.Addr) bool {
	if !peerIP.IsValid() || len(c.Prefixes) == 0 {
		return false
	}
	unmapped := peerIP.Unmap()
	for _, p := range c.Prefixes {
		if p.Contains(unmapped) {
			return true
		}
	}
	return false
}

// StripHopByHop removes RFC 9110 hop-by-hop headers and any headers nominated in Connection.
func StripHopByHop(h http.Header) {
	if h == nil {
		return
	}
	// First inspect Connection header values (case-insensitive) to strip nominated headers.
	for _, connVal := range h.Values("Connection") {
		for _, field := range strings.Split(connVal, ",") {
			name := strings.TrimSpace(field)
			if name != "" {
				h.Del(name)
			}
		}
	}
	for _, hdr := range HopByHopHeaders {
		if hdr == "Te" && strings.EqualFold(strings.TrimSpace(h.Get("Te")), "trailers") {
			continue
		}
		h.Del(hdr)
	}
}

// SanitizeOutboundHeaders strips hop-by-hop headers and enforces trusted-peer X-Forwarded-* rules.
func SanitizeOutboundHeaders(in, out *http.Request, trusted TrustedProxyConfig) {
	StripHopByHop(out.Header)

	peerAddr := extractPeerAddr(in.RemoteAddr)
	isTrustedPeer := trusted.IsTrusted(peerAddr)

	if !isTrustedPeer {
		// Untrusted client: purge all spoofable forwarded headers before setting authoritative values.
		out.Header.Del("X-Forwarded-For")
		out.Header.Del("X-Forwarded-Proto")
		out.Header.Del("X-Forwarded-Host")
		out.Header.Del("X-Real-Ip")
		out.Header.Del("Forwarded")

		if peerAddr.IsValid() {
			out.Header.Set("X-Forwarded-For", peerAddr.String())
		}
		if in.TLS != nil {
			out.Header.Set("X-Forwarded-Proto", "https")
		} else {
			out.Header.Set("X-Forwarded-Proto", "http")
		}
		if in.Host != "" {
			out.Header.Set("X-Forwarded-Host", in.Host)
		}
		return
	}

	// Trusted proxy peer: append peer IP to existing X-Forwarded-For chain and preserve proto/host when present.
	if peerAddr.IsValid() {
		prior := strings.TrimSpace(in.Header.Get("X-Forwarded-For"))
		if prior != "" {
			out.Header.Set("X-Forwarded-For", prior+", "+peerAddr.String())
		} else {
			out.Header.Set("X-Forwarded-For", peerAddr.String())
		}
	}
	if strings.TrimSpace(out.Header.Get("X-Forwarded-Proto")) == "" {
		if in.TLS != nil {
			out.Header.Set("X-Forwarded-Proto", "https")
		} else {
			out.Header.Set("X-Forwarded-Proto", "http")
		}
	}
	if strings.TrimSpace(out.Header.Get("X-Forwarded-Host")) == "" && in.Host != "" {
		out.Header.Set("X-Forwarded-Host", in.Host)
	}
}

func extractPeerAddr(remoteAddr string) netip.Addr {
	if remoteAddr == "" {
		return netip.Addr{}
	}
	host := remoteAddr
	if h, _, err := net.SplitHostPort(remoteAddr); err == nil {
		host = h
	}
	addr, err := netip.ParseAddr(strings.TrimSpace(host))
	if err != nil {
		return netip.Addr{}
	}
	return addr.Unmap()
}
