package metrics

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	DefaultMaxTrackedRoutes   = 256
	DefaultMaxTrackedServices = 128
	OverflowLabel             = "__overflow__"
)

var defaultHistogramBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0}

type reqCounterKey struct {
	routeID     string
	serviceID   string
	method      string
	statusClass string
	outcome     string
}

type histKey struct {
	routeID   string
	serviceID string
}

type histEntry struct {
	counts []uint64 // len == len(buckets), cumulative computed on render
	sum    float64
	total  uint64
}

type failKey struct {
	routeID   string
	serviceID string
	status    int
}

// Registry maintains Prometheus-compatible gateway data-plane metrics with strict cardinality caps.
// Only bounded route_id, service_id, HTTP method, status_class, and outcome labels are permitted;
// API keys, consumer IDs, tokens, IPs, and raw path parameters are NEVER accepted as labels.
type Registry struct {
	maxRoutes   int
	maxServices int
	buckets     []float64

	inFlight      atomic.Int64
	configVersion atomic.Int64

	mu              sync.RWMutex
	knownRoutes     map[string]struct{}
	knownServices   map[string]struct{}
	requestsTotal   map[reqCounterKey]uint64
	durationHist    map[histKey]*histEntry
	authFailures    map[failKey]uint64
	upstreamFailure map[failKey]uint64
}

// NewRegistry creates a bounded-cardinality Prometheus metrics registry for the gateway.
func NewRegistry(maxRoutes, maxServices int) *Registry {
	if maxRoutes <= 0 {
		maxRoutes = DefaultMaxTrackedRoutes
	}
	if maxServices <= 0 {
		maxServices = DefaultMaxTrackedServices
	}
	return &Registry{
		maxRoutes:       maxRoutes,
		maxServices:     maxServices,
		buckets:         defaultHistogramBuckets,
		knownRoutes:     make(map[string]struct{}),
		knownServices:   make(map[string]struct{}),
		requestsTotal:   make(map[reqCounterKey]uint64),
		durationHist:    make(map[histKey]*histEntry),
		authFailures:    make(map[failKey]uint64),
		upstreamFailure: make(map[failKey]uint64),
	}
}

// IncInFlight increments active in-flight gateway requests.
func (r *Registry) IncInFlight() {
	if r != nil {
		r.inFlight.Add(1)
	}
}

// DecInFlight decrements active in-flight gateway requests.
func (r *Registry) DecInFlight() {
	if r != nil {
		r.inFlight.Add(-1)
	}
}

// SetActiveConfigVersion updates the active published snapshot version gauge.
func (r *Registry) SetActiveConfigVersion(version int) {
	if r != nil {
		r.configVersion.Store(int64(version))
	}
}

// ObserveRequest records a completed gateway request while enforcing bounded label cardinality.
func (r *Registry) ObserveRequest(routeID, serviceID, method string, status int, outcome string, dur time.Duration) {
	if r == nil {
		return
	}
	cleanMethod := normalizeMethod(method)
	statusClass := fmt.Sprintf("%dxx", status/100)
	if status < 100 || status > 599 {
		statusClass = "other"
	}
	cleanOutcome := normalizeOutcomeLabel(outcome)
	sec := dur.Seconds()
	if sec < 0 {
		sec = 0
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	rtLabel := r.boundLabelLocked(routeID, r.knownRoutes, r.maxRoutes)
	svcLabel := r.boundLabelLocked(serviceID, r.knownServices, r.maxServices)

	rk := reqCounterKey{
		routeID:     rtLabel,
		serviceID:   svcLabel,
		method:      cleanMethod,
		statusClass: statusClass,
		outcome:     cleanOutcome,
	}
	r.requestsTotal[rk]++

	hk := histKey{routeID: rtLabel, serviceID: svcLabel}
	he, ok := r.durationHist[hk]
	if !ok {
		he = &histEntry{counts: make([]uint64, len(r.buckets))}
		r.durationHist[hk] = he
	}
	he.total++
	he.sum += sec
	for i, ub := range r.buckets {
		if sec <= ub {
			he.counts[i]++
		}
	}

	if status == 401 || status == 403 {
		r.authFailures[failKey{routeID: rtLabel, status: status}]++
	}
	if status >= 500 && status <= 599 {
		r.upstreamFailure[failKey{routeID: rtLabel, serviceID: svcLabel, status: status}]++
	}
}

func (r *Registry) boundLabelLocked(raw string, seen map[string]struct{}, limit int) string {
	clean := SanitizeLabel(raw)
	if clean == "unmatched" {
		return clean
	}
	if _, ok := seen[clean]; ok {
		return clean
	}
	if len(seen) >= limit {
		return OverflowLabel
	}
	seen[clean] = struct{}{}
	return clean
}

// TrackedCardinality returns the number of distinct route and service label values currently stored.
func (r *Registry) TrackedCardinality() (routes, services int) {
	if r == nil {
		return 0, 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.knownRoutes), len(r.knownServices)
}

// FormatPrometheus renders all gateway metrics in deterministic Prometheus exposition format.
func (r *Registry) FormatPrometheus(extraSections ...string) string {
	if r == nil {
		return ""
	}
	var b strings.Builder

	b.WriteString("# HELP gatewayforge_requests_in_flight Current number of in-flight gateway HTTP requests.\n")
	b.WriteString("# TYPE gatewayforge_requests_in_flight gauge\n")
	fmt.Fprintf(&b, "gatewayforge_requests_in_flight %d\n", r.inFlight.Load())

	b.WriteString("# HELP gatewayforge_active_config_version Active published configuration snapshot version loaded by this gateway.\n")
	b.WriteString("# TYPE gatewayforge_active_config_version gauge\n")
	fmt.Fprintf(&b, "gatewayforge_active_config_version %d\n", r.configVersion.Load())

	r.mu.RLock()
	reqKeys := make([]reqCounterKey, 0, len(r.requestsTotal))
	for k := range r.requestsTotal {
		reqKeys = append(reqKeys, k)
	}
	sort.Slice(reqKeys, func(i, j int) bool {
		if reqKeys[i].routeID != reqKeys[j].routeID {
			return reqKeys[i].routeID < reqKeys[j].routeID
		}
		if reqKeys[i].serviceID != reqKeys[j].serviceID {
			return reqKeys[i].serviceID < reqKeys[j].serviceID
		}
		if reqKeys[i].method != reqKeys[j].method {
			return reqKeys[i].method < reqKeys[j].method
		}
		if reqKeys[i].statusClass != reqKeys[j].statusClass {
			return reqKeys[i].statusClass < reqKeys[j].statusClass
		}
		return reqKeys[i].outcome < reqKeys[j].outcome
	})

	b.WriteString("# HELP gatewayforge_requests_total Total HTTP requests handled by the gateway.\n")
	b.WriteString("# TYPE gatewayforge_requests_total counter\n")
	for _, k := range reqKeys {
		fmt.Fprintf(&b, "gatewayforge_requests_total{route_id=%q,service_id=%q,method=%q,status_class=%q,outcome=%q} %d\n",
			k.routeID, k.serviceID, k.method, k.statusClass, k.outcome, r.requestsTotal[k])
	}

	hKeys := make([]histKey, 0, len(r.durationHist))
	for k := range r.durationHist {
		hKeys = append(hKeys, k)
	}
	sort.Slice(hKeys, func(i, j int) bool {
		if hKeys[i].routeID != hKeys[j].routeID {
			return hKeys[i].routeID < hKeys[j].routeID
		}
		return hKeys[i].serviceID < hKeys[j].serviceID
	})

	b.WriteString("# HELP gatewayforge_request_duration_seconds Gateway HTTP request duration histogram in seconds.\n")
	b.WriteString("# TYPE gatewayforge_request_duration_seconds histogram\n")
	for _, k := range hKeys {
		he := r.durationHist[k]
		for i, ub := range r.buckets {
			fmt.Fprintf(&b, "gatewayforge_request_duration_seconds_bucket{route_id=%q,service_id=%q,le=\"%g\"} %d\n",
				k.routeID, k.serviceID, ub, he.counts[i])
		}
		fmt.Fprintf(&b, "gatewayforge_request_duration_seconds_bucket{route_id=%q,service_id=%q,le=\"+Inf\"} %d\n",
			k.routeID, k.serviceID, he.total)
		fmt.Fprintf(&b, "gatewayforge_request_duration_seconds_sum{route_id=%q,service_id=%q} %.6f\n",
			k.routeID, k.serviceID, he.sum)
		fmt.Fprintf(&b, "gatewayforge_request_duration_seconds_count{route_id=%q,service_id=%q} %d\n",
			k.routeID, k.serviceID, he.total)
	}

	authKeys := make([]failKey, 0, len(r.authFailures))
	for k := range r.authFailures {
		authKeys = append(authKeys, k)
	}
	sort.Slice(authKeys, func(i, j int) bool {
		if authKeys[i].routeID != authKeys[j].routeID {
			return authKeys[i].routeID < authKeys[j].routeID
		}
		return authKeys[i].status < authKeys[j].status
	})
	b.WriteString("# HELP gatewayforge_auth_failures_total Total authentication and authorization denials.\n")
	b.WriteString("# TYPE gatewayforge_auth_failures_total counter\n")
	for _, k := range authKeys {
		fmt.Fprintf(&b, "gatewayforge_auth_failures_total{route_id=%q,status=\"%d\"} %d\n",
			k.routeID, k.status, r.authFailures[k])
	}

	upKeys := make([]failKey, 0, len(r.upstreamFailure))
	for k := range r.upstreamFailure {
		upKeys = append(upKeys, k)
	}
	sort.Slice(upKeys, func(i, j int) bool {
		if upKeys[i].routeID != upKeys[j].routeID {
			return upKeys[i].routeID < upKeys[j].routeID
		}
		if upKeys[i].serviceID != upKeys[j].serviceID {
			return upKeys[i].serviceID < upKeys[j].serviceID
		}
		return upKeys[i].status < upKeys[j].status
	})
	b.WriteString("# HELP gatewayforge_upstream_failures_total Total 5xx upstream/gateway failure responses.\n")
	b.WriteString("# TYPE gatewayforge_upstream_failures_total counter\n")
	for _, k := range upKeys {
		fmt.Fprintf(&b, "gatewayforge_upstream_failures_total{route_id=%q,service_id=%q,status=\"%d\"} %d\n",
			k.routeID, k.serviceID, k.status, r.upstreamFailure[k])
	}
	r.mu.RUnlock()

	for _, sec := range extraSections {
		if strings.TrimSpace(sec) != "" {
			b.WriteString(sec)
		}
	}
	return b.String()
}

// SanitizeLabel rejects unsafe or secret-like inputs and normalizes IDs for bounded Prometheus labels.
func SanitizeLabel(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "unmatched"
	}
	// Never permit API keys, JWTs, or raw paths with slashes/queries as metric labels.
	if strings.HasPrefix(s, "gwf_") || strings.HasPrefix(s, "eyJ") || strings.ContainsAny(s, "/?=& ") {
		return OverflowLabel
	}
	if len(s) > 64 {
		s = s[:64]
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
			b.WriteByte(c)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

func normalizeMethod(m string) string {
	switch strings.ToUpper(strings.TrimSpace(m)) {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
		return strings.ToUpper(strings.TrimSpace(m))
	default:
		return "OTHER"
	}
}

func normalizeOutcomeLabel(o string) string {
	switch o {
	case "ok", "client_error", "auth_denied", "rate_limited", "upstream_error", "timeout", "canceled":
		return o
	default:
		return "other"
	}
}
