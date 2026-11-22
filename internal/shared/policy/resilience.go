package policy

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// RetryPolicy configures safe, bounded idempotent upstream retries.
type RetryPolicy struct {
	MaxAttempts        int      `json:"max_attempts"`                    // 1..5 (total attempts including initial)
	BaseBackoffMS      int      `json:"base_backoff_ms,omitempty"`       // 10..5000 (default 50)
	MaxBackoffMS       int      `json:"max_backoff_ms,omitempty"`        // 10..30000 (default 1000)
	JitterFraction     float64  `json:"jitter_fraction,omitempty"`       // 0..0.5 (default 0.25)
	RetryableMethods   []string `json:"retryable_methods,omitempty"`     // default GET, HEAD, OPTIONS; POST/PATCH FORBIDDEN
	RetryableStatuses  []int    `json:"retryable_statuses,omitempty"`    // default 502, 503, 504
	MaxReplayBodyBytes int64    `json:"max_replay_body_bytes,omitempty"` // default 64 KiB, max 1 MiB
}

// TimeoutPolicy distinguishes upstream connect timeout vs total request timeout on a route.
type TimeoutPolicy struct {
	ConnectTimeoutMS int `json:"connect_timeout_ms,omitempty"` // 50..30000
	TotalTimeoutMS   int `json:"total_timeout_ms"`             // 100..120000
}

// CircuitBreakerPolicy configures closed/open/half_open circuit breaking per upstream pool or route.
type CircuitBreakerPolicy struct {
	FailureThreshold    int `json:"failure_threshold"`                 // 1..100 (default 5)
	SuccessThreshold    int `json:"success_threshold,omitempty"`       // 1..20 (default 2)
	OpenDurationMS      int `json:"open_duration_ms"`                  // 500..300000 (default 15000)
	HalfOpenMaxInFlight int `json:"half_open_max_in_flight,omitempty"` // 1..20 (default 2)
}

var allowedIdempotentMethods = map[string]bool{
	"GET":     true,
	"HEAD":    true,
	"OPTIONS": true,
	"PUT":     true,
	"DELETE":  true,
}

// ParseRetryPolicy decodes, normalizes, and validates a RetryPolicy.
func ParseRetryPolicy(raw json.RawMessage) (RetryPolicy, error) {
	var p RetryPolicy
	if err := json.Unmarshal(raw, &p); err != nil {
		return RetryPolicy{}, fmt.Errorf("invalid retry policy JSON: %w", err)
	}
	p.Normalize()
	if err := p.Validate(); err != nil {
		return RetryPolicy{}, err
	}
	return p, nil
}

// Normalize applies safe defaults to RetryPolicy.
func (p *RetryPolicy) Normalize() {
	if p.MaxAttempts == 0 {
		p.MaxAttempts = 1
	}
	if p.BaseBackoffMS == 0 {
		p.BaseBackoffMS = 50
	}
	if p.MaxBackoffMS == 0 {
		p.MaxBackoffMS = 1000
	}
	if p.JitterFraction == 0 {
		p.JitterFraction = 0.25
	}
	if len(p.RetryableMethods) == 0 {
		p.RetryableMethods = []string{"GET", "HEAD", "OPTIONS"}
	} else {
		for i, m := range p.RetryableMethods {
			p.RetryableMethods[i] = strings.ToUpper(strings.TrimSpace(m))
		}
	}
	if len(p.RetryableStatuses) == 0 {
		p.RetryableStatuses = []int{502, 503, 504}
	}
	if p.MaxReplayBodyBytes == 0 {
		p.MaxReplayBodyBytes = 64 << 10
	}
}

// Validate enforces strict retry safety bounds (never allowing automatic POST/PATCH retries).
func (p RetryPolicy) Validate() error {
	if p.MaxAttempts < 1 || p.MaxAttempts > 5 {
		return errors.New("max_attempts must be between 1 and 5")
	}
	if p.BaseBackoffMS < 10 || p.BaseBackoffMS > 5000 {
		return errors.New("base_backoff_ms must be between 10 and 5000")
	}
	if p.MaxBackoffMS < p.BaseBackoffMS || p.MaxBackoffMS > 30000 {
		return errors.New("max_backoff_ms must be >= base_backoff_ms and <= 30000")
	}
	if p.JitterFraction < 0 || p.JitterFraction > 0.5 {
		return errors.New("jitter_fraction must be between 0 and 0.5")
	}
	for _, m := range p.RetryableMethods {
		if !allowedIdempotentMethods[strings.ToUpper(strings.TrimSpace(m))] {
			return fmt.Errorf("method %q is non-idempotent and forbidden from automatic retries", m)
		}
	}
	for _, st := range p.RetryableStatuses {
		if st < 500 || st > 599 {
			return fmt.Errorf("retryable status %d must be a 5xx status code", st)
		}
	}
	if p.MaxReplayBodyBytes < 0 || p.MaxReplayBodyBytes > (1<<20) {
		return errors.New("max_replay_body_bytes must be between 0 and 1048576")
	}
	return nil
}

// ParseTimeoutPolicy decodes and validates a TimeoutPolicy.
func ParseTimeoutPolicy(raw json.RawMessage) (TimeoutPolicy, error) {
	var p TimeoutPolicy
	if err := json.Unmarshal(raw, &p); err != nil {
		return TimeoutPolicy{}, fmt.Errorf("invalid timeout policy JSON: %w", err)
	}
	if p.TotalTimeoutMS < 100 || p.TotalTimeoutMS > 120000 {
		return TimeoutPolicy{}, errors.New("total_timeout_ms must be between 100 and 120000")
	}
	if p.ConnectTimeoutMS != 0 && (p.ConnectTimeoutMS < 50 || p.ConnectTimeoutMS > 30000) {
		return TimeoutPolicy{}, errors.New("connect_timeout_ms must be between 50 and 30000")
	}
	if p.ConnectTimeoutMS > p.TotalTimeoutMS {
		return TimeoutPolicy{}, errors.New("connect_timeout_ms cannot exceed total_timeout_ms")
	}
	return p, nil
}

// ParseCircuitBreakerPolicy decodes, normalizes, and validates a CircuitBreakerPolicy.
func ParseCircuitBreakerPolicy(raw json.RawMessage) (CircuitBreakerPolicy, error) {
	var p CircuitBreakerPolicy
	if err := json.Unmarshal(raw, &p); err != nil {
		return CircuitBreakerPolicy{}, fmt.Errorf("invalid circuit_breaker policy JSON: %w", err)
	}
	if p.FailureThreshold == 0 {
		p.FailureThreshold = 5
	}
	if p.SuccessThreshold == 0 {
		p.SuccessThreshold = 2
	}
	if p.OpenDurationMS == 0 {
		p.OpenDurationMS = 15000
	}
	if p.HalfOpenMaxInFlight == 0 {
		p.HalfOpenMaxInFlight = 2
	}
	if p.FailureThreshold < 1 || p.FailureThreshold > 100 {
		return CircuitBreakerPolicy{}, errors.New("failure_threshold must be between 1 and 100")
	}
	if p.SuccessThreshold < 1 || p.SuccessThreshold > 20 {
		return CircuitBreakerPolicy{}, errors.New("success_threshold must be between 1 and 20")
	}
	if p.OpenDurationMS < 500 || p.OpenDurationMS > 300000 {
		return CircuitBreakerPolicy{}, errors.New("open_duration_ms must be between 500 and 300000")
	}
	if p.HalfOpenMaxInFlight < 1 || p.HalfOpenMaxInFlight > 20 {
		return CircuitBreakerPolicy{}, errors.New("half_open_max_in_flight must be between 1 and 20")
	}
	return p, nil
}
