package balancer

import (
	"errors"
	"sync"

	"github.com/YASSERRMD/GatewayForge/internal/shared/snapshot"
)

// ErrNoAvailableTargets is returned when an upstream pool has zero enabled healthy targets.
var ErrNoAvailableTargets = errors.New("no enabled healthy targets available")

// Selector picks an upstream target from a pool, optionally filtering by a health predicate.
type Selector interface {
	Pick(pool snapshot.UpstreamPool, isHealthy func(targetID string) bool) (snapshot.UpstreamTarget, error)
}

type poolState struct {
	mu      sync.Mutex
	weights map[string]int
}

// WeightedRoundRobin implements deterministic smooth weighted round-robin (Nginx-style SWRR)
// per pool ID, safe for concurrent goroutines.
type WeightedRoundRobin struct {
	mu    sync.Mutex
	pools map[string]*poolState
}

// NewWeightedRoundRobin creates a smooth weighted round-robin selector.
func NewWeightedRoundRobin() *WeightedRoundRobin {
	return &WeightedRoundRobin{
		pools: make(map[string]*poolState),
	}
}

func (w *WeightedRoundRobin) stateFor(poolID string) *poolState {
	w.mu.Lock()
	defer w.mu.Unlock()
	st, ok := w.pools[poolID]
	if !ok {
		st = &poolState{weights: make(map[string]int)}
		w.pools[poolID] = st
	}
	return st
}

// Pick selects the next enabled and healthy target according to smooth weighted round-robin.
func (w *WeightedRoundRobin) Pick(pool snapshot.UpstreamPool, isHealthy func(targetID string) bool) (snapshot.UpstreamTarget, error) {
	candidates := make([]snapshot.UpstreamTarget, 0, len(pool.Targets))
	totalWeight := 0
	for _, t := range pool.Targets {
		if !t.Enabled {
			continue
		}
		if isHealthy != nil && !isHealthy(t.ID) {
			continue
		}
		wt := t.Weight
		if wt <= 0 {
			wt = 1
		}
		cp := t
		cp.Weight = wt
		candidates = append(candidates, cp)
		totalWeight += wt
	}
	if len(candidates) == 0 {
		return snapshot.UpstreamTarget{}, ErrNoAvailableTargets
	}
	if len(candidates) == 1 {
		return candidates[0], nil
	}

	st := w.stateFor(pool.ID)
	st.mu.Lock()
	defer st.mu.Unlock()

	bestIdx := 0
	bestWeight := 0
	for i, c := range candidates {
		cw := st.weights[c.ID] + c.Weight
		st.weights[c.ID] = cw
		if i == 0 || cw > bestWeight {
			bestWeight = cw
			bestIdx = i
		}
	}

	chosen := candidates[bestIdx]
	st.weights[chosen.ID] -= totalWeight
	return chosen, nil
}
