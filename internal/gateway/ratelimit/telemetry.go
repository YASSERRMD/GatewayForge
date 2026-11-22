package ratelimit

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

const (
	DefaultMaxMetricRoutes = 256
	OverflowRouteLabel     = "__overflow__"
)

type metricKey struct {
	routeID string
	outcome string
	backend string
}

// TelemetrySnapshot provides an operator-facing bounded-cardinality view of limiter counters.
type TelemetrySnapshot struct {
	TotalAllowed    uint64            `json:"total_allowed"`
	TotalDenied     uint64            `json:"total_denied"`
	TotalErrors     uint64            `json:"total_errors"`
	TotalFailOpen   uint64            `json:"total_fail_open"`
	TotalFailClosed uint64            `json:"total_fail_closed"`
	TrackedRoutes   int               `json:"tracked_routes"`
	MaxRoutes       int               `json:"max_routes"`
	SeriesCount     int               `json:"series_count"`
	Degraded        bool              `json:"degraded"`
	ByOutcome       map[string]uint64 `json:"by_outcome"`
}

// Telemetry records bounded-cardinality rate-limit allow/deny/error counters.
// Labels are strictly restricted to (route_id, outcome, backend) with route_id capped at maxRoutes;
// consumer IDs, API keys, IPs, and path parameters are NEVER accepted as labels.
type Telemetry struct {
	maxRoutes int

	mu           sync.RWMutex
	knownRoutes  map[string]struct{}
	counters     map[metricKey]uint64
	lastDegraded bool
}

// NewTelemetry constructs a bounded-cardinality limiter telemetry collector.
func NewTelemetry(maxRoutes int) *Telemetry {
	if maxRoutes <= 0 {
		maxRoutes = DefaultMaxMetricRoutes
	}
	return &Telemetry{
		maxRoutes:   maxRoutes,
		knownRoutes: make(map[string]struct{}),
		counters:    make(map[metricKey]uint64),
	}
}

// Record increments the counter for (routeID, outcome, backend) while enforcing strict label bounds.
func (t *Telemetry) Record(routeID, outcome, backend string) {
	if t == nil {
		return
	}
	cleanOutcome := normalizeOutcome(outcome)
	cleanBackend := normalizeBackend(backend)
	cleanRoute := sanitizeLabelValue(routeID)

	t.mu.Lock()
	defer t.mu.Unlock()

	if _, ok := t.knownRoutes[cleanRoute]; !ok {
		if len(t.knownRoutes) >= t.maxRoutes {
			cleanRoute = OverflowRouteLabel
		} else {
			t.knownRoutes[cleanRoute] = struct{}{}
		}
	}

	k := metricKey{routeID: cleanRoute, outcome: cleanOutcome, backend: cleanBackend}
	t.counters[k]++
	if cleanOutcome == "allowed_fail_open" || cleanOutcome == "denied_fail_closed" || cleanOutcome == "error" {
		t.lastDegraded = true
	} else if cleanBackend == "redis" {
		t.lastDegraded = false
	}
}

// Snapshot returns an operator-facing JSON-serializable summary of limiter telemetry.
func (t *Telemetry) Snapshot() TelemetrySnapshot {
	if t == nil {
		return TelemetrySnapshot{}
	}
	t.mu.RLock()
	defer t.mu.RUnlock()

	snap := TelemetrySnapshot{
		TrackedRoutes: len(t.knownRoutes),
		MaxRoutes:     t.maxRoutes,
		SeriesCount:   len(t.counters),
		Degraded:      t.lastDegraded,
		ByOutcome:     make(map[string]uint64),
	}
	for k, v := range t.counters {
		snap.ByOutcome[k.outcome] += v
		switch k.outcome {
		case "allowed":
			snap.TotalAllowed += v
		case "denied":
			snap.TotalDenied += v
		case "error":
			snap.TotalErrors += v
		case "allowed_fail_open":
			snap.TotalFailOpen += v
		case "denied_fail_closed":
			snap.TotalFailClosed += v
		}
	}
	return snap
}

// FormatPrometheus renders deterministic Prometheus exposition format lines for limiter counters.
func (t *Telemetry) FormatPrometheus() string {
	if t == nil {
		return ""
	}
	t.mu.RLock()
	keys := make([]metricKey, 0, len(t.counters))
	for k := range t.counters {
		keys = append(keys, k)
	}
	vals := make(map[metricKey]uint64, len(t.counters))
	for k, v := range t.counters {
		vals[k] = v
	}
	t.mu.RUnlock()

	sort.Slice(keys, func(i, j int) bool {
		if keys[i].routeID != keys[j].routeID {
			return keys[i].routeID < keys[j].routeID
		}
		if keys[i].outcome != keys[j].outcome {
			return keys[i].outcome < keys[j].outcome
		}
		return keys[i].backend < keys[j].backend
	})

	var b strings.Builder
	b.WriteString("# HELP gatewayforge_ratelimit_decisions_total Total rate limit and quota decisions by bounded route, outcome, and backend.\n")
	b.WriteString("# TYPE gatewayforge_ratelimit_decisions_total counter\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "gatewayforge_ratelimit_decisions_total{route_id=%q,outcome=%q,backend=%q} %d\n",
			k.routeID, k.outcome, k.backend, vals[k])
	}
	return b.String()
}

func normalizeOutcome(o string) string {
	switch o {
	case "allowed", "denied", "error", "allowed_fail_open", "denied_fail_closed":
		return o
	default:
		return "other"
	}
}

func normalizeBackend(b string) string {
	switch b {
	case "redis", "local", "fail_open", "fail_closed", "invalid_policy":
		return b
	default:
		return "other"
	}
}

func sanitizeLabelValue(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "unknown"
	}
	if len(v) > 64 {
		v = v[:64]
	}
	var b strings.Builder
	b.Grow(len(v))
	for i := 0; i < len(v); i++ {
		c := v[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
			b.WriteByte(c)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}
