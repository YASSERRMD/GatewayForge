package resilience

import (
	"sync"
	"time"

	"github.com/YASSERRMD/GatewayForge/internal/shared/policy"
	"github.com/YASSERRMD/GatewayForge/internal/shared/snapshot"
)

// CircuitState represents the state of a circuit breaker.
type CircuitState string

const (
	CircuitClosed   CircuitState = "closed"
	CircuitOpen     CircuitState = "open"
	CircuitHalfOpen CircuitState = "half_open"
)

// ResolveCircuitBreakerPolicy returns the highest-precedence valid circuit_breaker policy attached to a route.
func ResolveCircuitBreakerPolicy(policies []snapshot.AttachedPolicy) (policy.CircuitBreakerPolicy, bool) {
	for _, ap := range policies {
		if ap.PolicyType != "circuit_breaker" {
			continue
		}
		p, err := policy.ParseCircuitBreakerPolicy(ap.Config)
		if err != nil {
			continue
		}
		return p, true
	}
	return policy.CircuitBreakerPolicy{}, false
}

type breaker struct {
	mu                  sync.Mutex
	state               CircuitState
	consecutiveFailures int
	halfOpenSuccesses   int
	halfOpenInFlight    int
	openedUntil         time.Time
}

// CircuitBreakerRegistry manages per-pool/route circuit breakers with bounded half-open probes.
type CircuitBreakerRegistry struct {
	now func() time.Time

	mu       sync.RWMutex
	breakers map[string]*breaker
}

// NewCircuitBreakerRegistry creates a CircuitBreakerRegistry with an optional injectable clock.
func NewCircuitBreakerRegistry(now func() time.Time) *CircuitBreakerRegistry {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &CircuitBreakerRegistry{
		now:      now,
		breakers: make(map[string]*breaker),
	}
}

func (r *CircuitBreakerRegistry) getOrCreate(key string) *breaker {
	r.mu.RLock()
	b, ok := r.breakers[key]
	r.mu.RUnlock()
	if ok {
		return b
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if b, ok = r.breakers[key]; ok {
		return b
	}
	b = &breaker{state: CircuitClosed}
	r.breakers[key] = b
	return b
}

// Allow checks whether a request may proceed through the circuit breaker for key.
// When allowed is true, callers MUST invoke the returned record callback once with the outcome.
func (r *CircuitBreakerRegistry) Allow(key string, pol policy.CircuitBreakerPolicy) (record func(statusCode int, err error), allowed bool) {
	if r == nil {
		return func(int, error) {}, true
	}
	b := r.getOrCreate(key)
	now := r.now()

	b.mu.Lock()
	switch b.state {
	case CircuitOpen:
		if now.Before(b.openedUntil) {
			b.mu.Unlock()
			return nil, false
		}
		// Open cooldown elapsed: transition to half_open.
		b.state = CircuitHalfOpen
		b.halfOpenSuccesses = 0
		b.halfOpenInFlight = 0
		fallthrough
	case CircuitHalfOpen:
		if b.halfOpenInFlight >= pol.HalfOpenMaxInFlight {
			b.mu.Unlock()
			return nil, false
		}
		b.halfOpenInFlight++
		b.mu.Unlock()
		var once sync.Once
		return func(statusCode int, err error) {
			once.Do(func() {
				r.finish(b, true, pol, statusCode, err)
			})
		}, true
	default: // CircuitClosed
		b.mu.Unlock()
		var once sync.Once
		return func(statusCode int, err error) {
			once.Do(func() {
				r.finish(b, false, pol, statusCode, err)
			})
		}, true
	}
}

func (r *CircuitBreakerRegistry) finish(b *breaker, wasHalfOpen bool, pol policy.CircuitBreakerPolicy, statusCode int, err error) {
	now := r.now()
	failed := IsUpstreamHealthFailure(statusCode, err)

	b.mu.Lock()
	defer b.mu.Unlock()

	if wasHalfOpen && b.halfOpenInFlight > 0 {
		b.halfOpenInFlight--
	}

	switch b.state {
	case CircuitHalfOpen:
		if failed {
			b.state = CircuitOpen
			b.openedUntil = now.Add(time.Duration(pol.OpenDurationMS) * time.Millisecond)
			b.halfOpenSuccesses = 0
			return
		}
		b.halfOpenSuccesses++
		if b.halfOpenSuccesses >= pol.SuccessThreshold {
			b.state = CircuitClosed
			b.consecutiveFailures = 0
			b.halfOpenSuccesses = 0
			b.openedUntil = time.Time{}
		}
	case CircuitClosed:
		if failed {
			b.consecutiveFailures++
			if b.consecutiveFailures >= pol.FailureThreshold {
				b.state = CircuitOpen
				b.openedUntil = now.Add(time.Duration(pol.OpenDurationMS) * time.Millisecond)
			}
		} else {
			b.consecutiveFailures = 0
		}
	}
}

// State returns the current effective circuit state for key.
func (r *CircuitBreakerRegistry) State(key string) CircuitState {
	if r == nil {
		return CircuitClosed
	}
	r.mu.RLock()
	b, ok := r.breakers[key]
	r.mu.RUnlock()
	if !ok {
		return CircuitClosed
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state == CircuitOpen && !r.now().Before(b.openedUntil) {
		return CircuitHalfOpen
	}
	return b.state
}
