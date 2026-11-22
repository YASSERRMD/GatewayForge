package ratelimit

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"

	"github.com/YASSERRMD/GatewayForge/internal/shared/policy"
)

const maxRawDimensionLen = 256

// KeyInput supplies tenant, route, policy, and request identity metadata for deterministic bucket key derivation.
type KeyInput struct {
	OrgID        string
	PolicyID     string
	ConfigDigest string
	ScopeType    string // org | service | route
	ServiceID    string
	RouteID      string
	ConsumerID   string // authenticated consumer/sub ID if present
	ClientIP     string
	Request      *http.Request
}

// DeriveRateLimitKey computes a collision-free, tenant-isolated, secret-free Redis bucket key.
// Raw secrets, headers, and IPs are NEVER embedded verbatim in the returned key.
func DeriveRateLimitKey(in KeyInput, pol policy.RateLimitPolicy) string {
	pol.Normalize()
	scopePart := scopeSegment(in)
	dimTag, subject := extractDimension(in, pol.KeyBy, pol.HeaderName)
	subjectHash := hashSubject(in.OrgID, dimTag, subject)
	digest := in.ConfigDigest
	if digest == "" {
		digest = pol.ConfigDigest()
	}
	return fmt.Sprintf("gwf:rl:%s:%s:%s:%s:%s",
		sanitizeSegment(in.OrgID),
		sanitizeSegment(in.PolicyID),
		sanitizeSegment(digest),
		scopePart,
		dimTag+"_"+subjectHash,
	)
}

// DeriveQuotaKey computes a collision-free, tenant-isolated, UTC-period-scoped Redis quota key.
func DeriveQuotaKey(in KeyInput, q policy.QuotaPolicy, periodBucket string) string {
	q.Normalize()
	scopePart := scopeSegment(in)
	dimTag, subject := extractDimension(in, q.KeyBy, "")
	subjectHash := hashSubject(in.OrgID, dimTag, subject)
	digest := in.ConfigDigest
	if digest == "" {
		digest = q.ConfigDigest()
	}
	return fmt.Sprintf("gwf:q:%s:%s:%s:%s:%s:%s",
		sanitizeSegment(in.OrgID),
		sanitizeSegment(in.PolicyID),
		sanitizeSegment(digest),
		scopePart,
		sanitizeSegment(periodBucket),
		dimTag+"_"+subjectHash,
	)
}

func scopeSegment(in KeyInput) string {
	switch in.ScopeType {
	case "org":
		return "org"
	case "service":
		return "svc_" + sanitizeSegment(in.ServiceID)
	default:
		return "rt_" + sanitizeSegment(in.RouteID)
	}
}

func extractDimension(in KeyInput, keyBy, headerName string) (string, string) {
	switch keyBy {
	case policy.KeyByOrg:
		return "org", in.OrgID
	case policy.KeyByRoute:
		return "route", in.RouteID
	case policy.KeyByConsumer:
		if strings.TrimSpace(in.ConsumerID) != "" {
			return "consumer", strings.TrimSpace(in.ConsumerID)
		}
		// Unauthenticated consumer falls back to normalized client IP under a separate tag
		// so anonymous traffic cannot collide with authenticated consumer IDs.
		return "anon_ip", normalizeClientIP(in)
	case policy.KeyByHeader:
		val := ""
		if in.Request != nil && headerName != "" {
			val = strings.TrimSpace(in.Request.Header.Get(headerName))
		}
		if len(val) > maxRawDimensionLen {
			val = val[:maxRawDimensionLen]
		}
		if val == "" {
			return "hdr_empty", normalizeClientIP(in)
		}
		return "hdr", strings.ToLower(headerName) + "=" + val
	default: // policy.KeyByIP
		return "ip", normalizeClientIP(in)
	}
}

func normalizeClientIP(in KeyInput) string {
	raw := strings.TrimSpace(in.ClientIP)
	if raw == "" && in.Request != nil {
		raw = strings.TrimSpace(in.Request.RemoteAddr)
	}
	if host, _, err := net.SplitHostPort(raw); err == nil {
		raw = host
	}
	if addr, err := netip.ParseAddr(raw); err == nil {
		return addr.Unmap().String()
	}
	if len(raw) > maxRawDimensionLen {
		raw = raw[:maxRawDimensionLen]
	}
	if raw == "" {
		return "unknown"
	}
	return raw
}

func hashSubject(orgID, dimTag, subject string) string {
	h := sha256.New()
	_, _ = h.Write([]byte(orgID))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(dimTag))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(subject))
	sum := h.Sum(nil)
	return hex.EncodeToString(sum[:16]) // 128-bit collision-resistant hex
}

func sanitizeSegment(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "none"
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s) && i < 64; i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
			b.WriteByte(c)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}
