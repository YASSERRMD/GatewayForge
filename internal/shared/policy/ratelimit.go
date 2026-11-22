package policy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	AlgoTokenBucket = "token_bucket"
	AlgoGCRA        = "gcra"

	KeyByIP       = "ip"
	KeyByConsumer = "consumer"
	KeyByOrg      = "org"
	KeyByRoute    = "route"
	KeyByHeader   = "header"

	FailOpen   = "fail_open"
	FailClosed = "fail_closed"

	ClassStandard = "standard"
	ClassCritical = "critical"

	PeriodHourly  = "hourly"
	PeriodDaily   = "daily"
	PeriodMonthly = "monthly"
)

// RateLimitPolicy defines a typed rate-limiting configuration for a route, service, or org.
type RateLimitPolicy struct {
	Algorithm         string `json:"algorithm,omitempty"`       // token_bucket (default) | gcra
	KeyBy             string `json:"key_by"`                    // ip | consumer | org | route | header
	HeaderName        string `json:"header_name,omitempty"`     // required when key_by == header
	RequestsPerWindow int64  `json:"requests_per_window"`       // 1..1_000_000
	WindowSeconds     int64  `json:"window_seconds"`            // 1..86400
	Burst             int64  `json:"burst,omitempty"`           // defaults to RequestsPerWindow
	FailMode          string `json:"fail_mode,omitempty"`       // fail_open | fail_closed
	SecurityClass     string `json:"security_class,omitempty"`  // standard | critical
	IncludeHeaders    *bool  `json:"include_headers,omitempty"` // default true
}

// QuotaPolicy defines a UTC period quota configuration (hourly/daily/monthly).
type QuotaPolicy struct {
	KeyBy         string `json:"key_by"`                   // consumer | org | ip
	Limit         int64  `json:"limit"`                    // 1..100_000_000
	Period        string `json:"period"`                   // hourly | daily | monthly (UTC)
	FailMode      string `json:"fail_mode,omitempty"`      // fail_open | fail_closed
	SecurityClass string `json:"security_class,omitempty"` // standard | critical
}

// ParseRateLimitPolicy decodes, normalizes defaults, and validates a rate-limit policy JSON object.
func ParseRateLimitPolicy(raw json.RawMessage) (RateLimitPolicy, error) {
	var p RateLimitPolicy
	if err := json.Unmarshal(raw, &p); err != nil {
		return RateLimitPolicy{}, fmt.Errorf("invalid rate_limit JSON: %w", err)
	}
	p.Normalize()
	if err := p.Validate(); err != nil {
		return RateLimitPolicy{}, err
	}
	return p, nil
}

// Normalize fills safe defaults on RateLimitPolicy before validation or execution.
func (p *RateLimitPolicy) Normalize() {
	p.Algorithm = strings.ToLower(strings.TrimSpace(p.Algorithm))
	if p.Algorithm == "" {
		p.Algorithm = AlgoTokenBucket
	}
	p.KeyBy = strings.ToLower(strings.TrimSpace(p.KeyBy))
	if p.KeyBy == "" {
		p.KeyBy = KeyByConsumer
	}
	p.HeaderName = strings.TrimSpace(p.HeaderName)
	if p.Burst == 0 && p.RequestsPerWindow > 0 {
		p.Burst = p.RequestsPerWindow
	}
	p.SecurityClass = strings.ToLower(strings.TrimSpace(p.SecurityClass))
	if p.SecurityClass == "" {
		p.SecurityClass = ClassStandard
	}
	p.FailMode = strings.ToLower(strings.TrimSpace(p.FailMode))
	if p.FailMode == "" {
		if p.SecurityClass == ClassCritical {
			p.FailMode = FailClosed
		} else {
			p.FailMode = FailOpen
		}
	}
}

// EmitHeaders reports whether RateLimit/Retry-After headers should be emitted (default true).
func (p RateLimitPolicy) EmitHeaders() bool {
	return p.IncludeHeaders == nil || *p.IncludeHeaders
}

// Validate enforces strict bounds and fail-mode security invariants on RateLimitPolicy.
func (p RateLimitPolicy) Validate() error {
	switch p.Algorithm {
	case AlgoTokenBucket, AlgoGCRA:
	default:
		return fmt.Errorf("unsupported algorithm %q (must be token_bucket or gcra)", p.Algorithm)
	}
	switch p.KeyBy {
	case KeyByIP, KeyByConsumer, KeyByOrg, KeyByRoute:
	case KeyByHeader:
		if p.HeaderName == "" || len(p.HeaderName) > 64 || strings.ContainsAny(p.HeaderName, " \t\r\n:") {
			return errors.New("header_name must be a valid non-empty HTTP header name when key_by=header")
		}
	default:
		return fmt.Errorf("unsupported key_by %q", p.KeyBy)
	}
	if p.RequestsPerWindow < 1 || p.RequestsPerWindow > 1_000_000 {
		return errors.New("requests_per_window must be between 1 and 1000000")
	}
	if p.WindowSeconds < 1 || p.WindowSeconds > 86400 {
		return errors.New("window_seconds must be between 1 and 86400")
	}
	if p.Burst < 1 || p.Burst > 1_000_000 {
		return errors.New("burst must be between 1 and 1000000")
	}
	if p.Burst < p.RequestsPerWindow {
		return errors.New("burst must be greater than or equal to requests_per_window")
	}
	switch p.SecurityClass {
	case ClassStandard, ClassCritical:
	default:
		return fmt.Errorf("unsupported security_class %q (must be standard or critical)", p.SecurityClass)
	}
	switch p.FailMode {
	case FailOpen, FailClosed:
	default:
		return fmt.Errorf("unsupported fail_mode %q (must be fail_open or fail_closed)", p.FailMode)
	}
	if p.SecurityClass == ClassCritical && p.FailMode == FailOpen {
		return errors.New("critical security_class cannot use fail_open; fail_closed is required")
	}
	return nil
}

// ConfigDigest returns a deterministic 12-hex-char fingerprint of the normalized limiter parameters.
// Changing rate, window, burst, algorithm, or key dimension changes the digest so stale Redis buckets
// cannot grant privilege escalation after a config update.
func (p RateLimitPolicy) ConfigDigest() string {
	canonical := fmt.Sprintf("%s|%s|%s|%d|%d|%d",
		p.Algorithm, p.KeyBy, strings.ToLower(p.HeaderName),
		p.RequestsPerWindow, p.WindowSeconds, p.Burst,
	)
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:6])
}

// ParseQuotaPolicy decodes, normalizes defaults, and validates a UTC quota policy JSON object.
func ParseQuotaPolicy(raw json.RawMessage) (QuotaPolicy, error) {
	var q QuotaPolicy
	if err := json.Unmarshal(raw, &q); err != nil {
		return QuotaPolicy{}, fmt.Errorf("invalid quota JSON: %w", err)
	}
	q.Normalize()
	if err := q.Validate(); err != nil {
		return QuotaPolicy{}, err
	}
	return q, nil
}

// Normalize fills safe defaults on QuotaPolicy.
func (q *QuotaPolicy) Normalize() {
	q.KeyBy = strings.ToLower(strings.TrimSpace(q.KeyBy))
	if q.KeyBy == "" {
		q.KeyBy = KeyByConsumer
	}
	q.Period = strings.ToLower(strings.TrimSpace(q.Period))
	if q.Period == "" {
		q.Period = PeriodDaily
	}
	q.SecurityClass = strings.ToLower(strings.TrimSpace(q.SecurityClass))
	if q.SecurityClass == "" {
		q.SecurityClass = ClassStandard
	}
	q.FailMode = strings.ToLower(strings.TrimSpace(q.FailMode))
	if q.FailMode == "" {
		if q.SecurityClass == ClassCritical {
			q.FailMode = FailClosed
		} else {
			q.FailMode = FailOpen
		}
	}
}

// Validate enforces bounds and fail-mode safety on QuotaPolicy.
func (q QuotaPolicy) Validate() error {
	switch q.KeyBy {
	case KeyByConsumer, KeyByOrg, KeyByIP:
	default:
		return fmt.Errorf("unsupported quota key_by %q (must be consumer, org, or ip)", q.KeyBy)
	}
	if q.Limit < 1 || q.Limit > 100_000_000 {
		return errors.New("quota limit must be between 1 and 100000000")
	}
	switch q.Period {
	case PeriodHourly, PeriodDaily, PeriodMonthly:
	default:
		return fmt.Errorf("unsupported quota period %q (must be hourly, daily, or monthly)", q.Period)
	}
	switch q.SecurityClass {
	case ClassStandard, ClassCritical:
	default:
		return fmt.Errorf("unsupported security_class %q", q.SecurityClass)
	}
	switch q.FailMode {
	case FailOpen, FailClosed:
	default:
		return fmt.Errorf("unsupported fail_mode %q", q.FailMode)
	}
	if q.SecurityClass == ClassCritical && q.FailMode == FailOpen {
		return errors.New("critical security_class quota cannot use fail_open")
	}
	return nil
}

// ConfigDigest returns a deterministic 12-hex-char fingerprint of the normalized quota parameters.
func (q QuotaPolicy) ConfigDigest() string {
	canonical := fmt.Sprintf("quota|%s|%s|%d", q.KeyBy, q.Period, q.Limit)
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:6])
}
