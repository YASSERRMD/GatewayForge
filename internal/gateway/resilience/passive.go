package resilience

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// PassiveConfig configures passive failure streak tracking and temporary ejection cooldown.
type PassiveConfig struct {
	ConsecutiveFailures int           // default 3
	Cooldown            time.Duration // default 15s
}

// DefaultPassiveConfig returns safe passive ejection defaults.
func DefaultPassiveConfig() PassiveConfig {
	return PassiveConfig{
		ConsecutiveFailures: 3,
		Cooldown:            15 * time.Second,
	}
}

// IsUpstreamHealthFailure classifies whether a transport error or HTTP status code represents
// an upstream health failure. Expected business 4xx responses and client cancellations are NEVER
// treated as upstream failures.
func IsUpstreamHealthFailure(statusCode int, err error) bool {
	if err != nil {
		return !errors.Is(err, context.Canceled)
	}
	// 2xx, 3xx, and 4xx (business/auth/validation errors) indicate a responsive upstream.
	if statusCode >= 200 && statusCode < 500 {
		return false
	}
	// 502, 503, 504 indicate upstream failure/overload/timeout.
	return statusCode == http.StatusBadGateway ||
		statusCode == http.StatusServiceUnavailable ||
		statusCode == http.StatusGatewayTimeout
}

// RecordPassiveOutcome updates a target's passive health state from a live request outcome.
func (h *HealthTracker) RecordPassiveOutcome(poolID, targetID string, statusCode int, err error, cfg PassiveConfig) {
	if h == nil {
		return
	}
	if cfg.ConsecutiveFailures <= 0 {
		cfg.ConsecutiveFailures = DefaultPassiveConfig().ConsecutiveFailures
	}
	if cfg.Cooldown <= 0 {
		cfg.Cooldown = DefaultPassiveConfig().Cooldown
	}

	// Client-initiated cancellation never counts for or against upstream health.
	if err != nil && errors.Is(err, context.Canceled) {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	rec, ok := h.targets[targetKey(poolID, targetID)]
	if !ok || !rec.Enabled || rec.Status == StatusDisabled {
		return
	}

	now := h.now()
	if !IsUpstreamHealthFailure(statusCode, err) {
		rec.ConsecutiveFailures = 0
		rec.ConsecutiveSuccess++
		rec.LastError = ""
		if rec.Status == StatusUnhealthy {
			rec.Status = StatusHealthy
			rec.PassiveOpenUntil = time.Time{}
		}
		return
	}

	rec.ConsecutiveSuccess = 0
	rec.ConsecutiveFailures++
	rec.LastFailureAt = now
	if err != nil {
		rec.LastError = err.Error()
	} else {
		rec.LastError = http.StatusText(statusCode)
	}
	if rec.ConsecutiveFailures >= cfg.ConsecutiveFailures {
		rec.Status = StatusUnhealthy
		rec.PassiveOpenUntil = now.Add(cfg.Cooldown)
	}
}
