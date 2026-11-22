package resilience

import (
	"encoding/json"
	"sync"

	"github.com/YASSERRMD/GatewayForge/internal/shared/snapshot"
)

type bulkheadBucket struct {
	mu       sync.Mutex
	inFlight int
}

// BulkheadRegistry enforces bounded per-route or per-upstream concurrency limits (fail-fast overload protection).
type BulkheadRegistry struct {
	defaultLimit int

	mu      sync.RWMutex
	buckets map[string]*bulkheadBucket
}

// NewBulkheadRegistry constructs a BulkheadRegistry with defaultLimit (default 256 if <= 0).
func NewBulkheadRegistry(defaultLimit int) *BulkheadRegistry {
	if defaultLimit <= 0 {
		defaultLimit = 256
	}
	return &BulkheadRegistry{
		defaultLimit: defaultLimit,
		buckets:      make(map[string]*bulkheadBucket),
	}
}

type rawBulkheadConfig struct {
	MaxConcurrent int `json:"max_concurrent"`
}

// ResolveMaxConcurrent extracts an explicit max_concurrent bulkhead limit from route policies,
// or falls back to the registry default when none is attached.
func ResolveMaxConcurrent(policies []snapshot.AttachedPolicy, fallback int) int {
	for _, ap := range policies {
		var raw rawBulkheadConfig
		if err := json.Unmarshal(ap.Config, &raw); err == nil && raw.MaxConcurrent > 0 {
			return raw.MaxConcurrent
		}
	}
	return fallback
}

func (r *BulkheadRegistry) bucket(key string) *bulkheadBucket {
	r.mu.RLock()
	b, ok := r.buckets[key]
	r.mu.RUnlock()
	if ok {
		return b
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if b, ok = r.buckets[key]; ok {
		return b
	}
	b = &bulkheadBucket{}
	r.buckets[key] = b
	return b
}

// Acquire tries to claim one in-flight slot for key up to limit without blocking.
// Callers MUST invoke release once when allowed is true.
func (r *BulkheadRegistry) Acquire(key string, limit int) (release func(), allowed bool) {
	if r == nil {
		return func() {}, true
	}
	if limit <= 0 {
		limit = r.defaultLimit
	}
	b := r.bucket(key)
	b.mu.Lock()
	if b.inFlight >= limit {
		b.mu.Unlock()
		return nil, false
	}
	b.inFlight++
	b.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			b.mu.Lock()
			if b.inFlight > 0 {
				b.inFlight--
			}
			b.mu.Unlock()
		})
	}, true
}

// InFlight returns the current number of active in-flight requests for key.
func (r *BulkheadRegistry) InFlight(key string) int {
	if r == nil {
		return 0
	}
	r.mu.RLock()
	b, ok := r.buckets[key]
	r.mu.RUnlock()
	if !ok {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.inFlight
}
