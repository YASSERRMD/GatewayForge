package resilience

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/YASSERRMD/GatewayForge/internal/shared/snapshot"
)

// TargetStatus describes the current health classification of a single upstream target.
type TargetStatus string

const (
	StatusHealthy   TargetStatus = "healthy"
	StatusUnhealthy TargetStatus = "unhealthy"
	StatusDisabled  TargetStatus = "disabled"
)

// ActiveProbeConfig configures bounded, jittered active upstream health checks.
type ActiveProbeConfig struct {
	Interval           time.Duration
	Timeout            time.Duration
	JitterFraction     float64 // 0.0..0.5
	HealthyThreshold   int
	UnhealthyThreshold int
	Path               string
}

// DefaultActiveProbeConfig returns safe active health probe defaults.
func DefaultActiveProbeConfig() ActiveProbeConfig {
	return ActiveProbeConfig{
		Interval:           10 * time.Second,
		Timeout:            2 * time.Second,
		JitterFraction:     0.2,
		HealthyThreshold:   2,
		UnhealthyThreshold: 3,
		Path:               "/health",
	}
}

// TargetRecord tracks active and passive health state for one upstream target.
type TargetRecord struct {
	PoolID              string
	TargetID            string
	URL                 string
	Enabled             bool
	Status              TargetStatus
	ConsecutiveSuccess  int
	ConsecutiveFailures int
	LastProbeAt         time.Time
	LastFailureAt       time.Time
	LastError           string
	PassiveOpenUntil    time.Time
}

// ProbeFunc executes a single bounded health probe against targetURL + path.
type ProbeFunc func(ctx context.Context, targetURL, path string) error

// HealthTracker manages active probe state and passive failure tracking for all configured pools.
type HealthTracker struct {
	cfg   ActiveProbeConfig
	probe ProbeFunc
	now   func() time.Time
	randF func() float64 // returns [0, 1)

	mu      sync.RWMutex
	targets map[string]*TargetRecord // key: poolID + "|" + targetID
}

// NewHealthTracker creates a HealthTracker with bounded HTTP probing (or custom probeFn for tests).
func NewHealthTracker(cfg ActiveProbeConfig, transport http.RoundTripper, probeFn ProbeFunc) *HealthTracker {
	def := DefaultActiveProbeConfig()
	if cfg.Interval <= 0 {
		cfg.Interval = def.Interval
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = def.Timeout
	}
	if cfg.JitterFraction < 0 || cfg.JitterFraction > 0.5 {
		cfg.JitterFraction = def.JitterFraction
	}
	if cfg.HealthyThreshold <= 0 {
		cfg.HealthyThreshold = def.HealthyThreshold
	}
	if cfg.UnhealthyThreshold <= 0 {
		cfg.UnhealthyThreshold = def.UnhealthyThreshold
	}
	if cfg.Path == "" {
		cfg.Path = def.Path
	}

	if probeFn == nil {
		client := &http.Client{
			Transport: transport,
			Timeout:   cfg.Timeout,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
		probeFn = func(ctx context.Context, targetURL, path string) error {
			probeURL := strings.TrimRight(targetURL, "/") + "/" + strings.TrimLeft(path, "/")
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL, nil)
			if err != nil {
				return err
			}
			resp, err := client.Do(req)
			if err != nil {
				return err
			}
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 400 {
				return nil
			}
			return fmt.Errorf("probe status %d", resp.StatusCode)
		}
	}

	return &HealthTracker{
		cfg:     cfg,
		probe:   probeFn,
		now:     func() time.Time { return time.Now().UTC() },
		randF:   cryptoRandFloat64,
		targets: make(map[string]*TargetRecord),
	}
}

// SetClock overrides the clock used by HealthTracker (primarily for deterministic tests).
func (h *HealthTracker) SetClock(fn func() time.Time) {
	if fn != nil {
		h.mu.Lock()
		h.now = fn
		h.mu.Unlock()
	}
}

func targetKey(poolID, targetID string) string {
	return poolID + "|" + targetID
}

// SyncPools reconciles tracked targets with the latest compiled upstream pools.
// Administratively disabled targets are always marked StatusDisabled and never routed to.
func (h *HealthTracker) SyncPools(pools map[string]snapshot.UpstreamPool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	present := make(map[string]struct{})
	for poolID, p := range pools {
		for _, t := range p.Targets {
			k := targetKey(poolID, t.ID)
			present[k] = struct{}{}
			rec, ok := h.targets[k]
			if !ok {
				st := StatusHealthy
				if !t.Enabled {
					st = StatusDisabled
				}
				h.targets[k] = &TargetRecord{
					PoolID:   poolID,
					TargetID: t.ID,
					URL:      t.URL,
					Enabled:  t.Enabled,
					Status:   st,
				}
				continue
			}
			rec.URL = t.URL
			rec.Enabled = t.Enabled
			if !t.Enabled {
				rec.Status = StatusDisabled
			} else if rec.Status == StatusDisabled {
				rec.Status = StatusHealthy
				rec.ConsecutiveFailures = 0
				rec.ConsecutiveSuccess = 0
			}
		}
	}
	for k := range h.targets {
		if _, ok := present[k]; !ok {
			delete(h.targets, k)
		}
	}
}

// ProbeAll runs one bounded probe pass over every administratively enabled target.
func (h *HealthTracker) ProbeAll(ctx context.Context) {
	h.mu.RLock()
	type item struct {
		key string
		url string
	}
	var list []item
	for k, rec := range h.targets {
		if rec.Enabled {
			list = append(list, item{key: k, url: rec.URL})
		}
	}
	h.mu.RUnlock()

	for _, it := range list {
		if ctx.Err() != nil {
			return
		}
		pCtx, cancel := context.WithTimeout(ctx, h.cfg.Timeout)
		err := h.probe(pCtx, it.url, h.cfg.Path)
		cancel()
		h.recordActiveProbeResult(it.key, err)
	}
}

func (h *HealthTracker) recordActiveProbeResult(key string, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	rec, ok := h.targets[key]
	if !ok || !rec.Enabled {
		return
	}
	now := h.now()
	rec.LastProbeAt = now

	if err == nil {
		rec.ConsecutiveFailures = 0
		rec.ConsecutiveSuccess++
		rec.LastError = ""
		if rec.Status == StatusUnhealthy && rec.ConsecutiveSuccess >= h.cfg.HealthyThreshold {
			rec.Status = StatusHealthy
			rec.PassiveOpenUntil = time.Time{}
		}
		return
	}

	rec.ConsecutiveSuccess = 0
	rec.ConsecutiveFailures++
	rec.LastFailureAt = now
	rec.LastError = err.Error()
	if rec.ConsecutiveFailures >= h.cfg.UnhealthyThreshold {
		rec.Status = StatusUnhealthy
	}
}

// NextJitteredInterval returns the next active probe interval with symmetric bounded jitter.
func (h *HealthTracker) NextJitteredInterval() time.Duration {
	if h.cfg.JitterFraction <= 0 {
		return h.cfg.Interval
	}
	// Map randF in [0,1) -> [-JitterFraction, +JitterFraction]
	delta := (h.randF()*2.0 - 1.0) * h.cfg.JitterFraction
	d := time.Duration(float64(h.cfg.Interval) * (1.0 + delta))
	if d < 50*time.Millisecond {
		return 50 * time.Millisecond
	}
	return d
}

// RunActiveLoop periodically probes enabled targets until ctx is canceled.
func (h *HealthTracker) RunActiveLoop(ctx context.Context) {
	timer := time.NewTimer(h.NextJitteredInterval())
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			h.ProbeAll(ctx)
			timer.Reset(h.NextJitteredInterval())
		}
	}
}

// IsHealthy reports whether (poolID, targetID) is enabled and currently healthy.
func (h *HealthTracker) IsHealthy(poolID, targetID string) bool {
	if h == nil {
		return true
	}
	h.mu.RLock()
	defer h.mu.RUnlock()

	rec, ok := h.targets[targetKey(poolID, targetID)]
	if !ok {
		return true
	}
	if !rec.Enabled || rec.Status == StatusDisabled {
		return false
	}
	if rec.Status == StatusUnhealthy {
		// If marked unhealthy purely by passive cooldown and cooldown has expired, allow trial traffic.
		if !rec.PassiveOpenUntil.IsZero() && !h.now().Before(rec.PassiveOpenUntil) {
			return true
		}
		return false
	}
	return true
}

// RecordSnapshot returns a safe copy of a target's health record.
func (h *HealthTracker) RecordSnapshot(poolID, targetID string) (TargetRecord, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	rec, ok := h.targets[targetKey(poolID, targetID)]
	if !ok {
		return TargetRecord{}, false
	}
	return *rec, true
}

func cryptoRandFloat64() float64 {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0.5
	}
	u := binary.BigEndian.Uint64(b[:]) >> 11
	return float64(u) / float64(uint64(1)<<53)
}

// ErrAllTargetsUnhealthy is returned when no enabled healthy targets exist in an upstream pool.
var ErrAllTargetsUnhealthy = errors.New("all upstream targets are unhealthy or disabled")
