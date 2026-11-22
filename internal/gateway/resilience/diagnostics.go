package resilience

import (
	"sort"
	"time"
)

// TargetDiagnostic is a safe, credential-free status summary for a single upstream target.
// Raw internal URLs, IPs, and credentials are intentionally omitted.
type TargetDiagnostic struct {
	TargetID            string       `json:"target_id"`
	Status              TargetStatus `json:"status"`
	Enabled             bool         `json:"enabled"`
	ConsecutiveFailures int          `json:"consecutive_failures"`
	LastProbeAt         *time.Time   `json:"last_probe_at,omitempty"`
}

// PoolDiagnostic summarizes aggregated upstream health for a single pool.
type PoolDiagnostic struct {
	PoolID           string             `json:"pool_id"`
	Status           string             `json:"status"` // healthy | degraded | unavailable
	HealthyTargets   int                `json:"healthy_targets"`
	UnhealthyTargets int                `json:"unhealthy_targets"`
	DisabledTargets  int                `json:"disabled_targets"`
	TotalTargets     int                `json:"total_targets"`
	Targets          []TargetDiagnostic `json:"targets,omitempty"`
}

// DiagnosticsSnapshot returns a safe aggregated view of all tracked upstream pools.
// When includeTargets is false, only pool-level aggregated counts are emitted.
func (h *HealthTracker) DiagnosticsSnapshot(includeTargets bool) []PoolDiagnostic {
	if h == nil {
		return []PoolDiagnostic{}
	}
	h.mu.RLock()
	byPool := make(map[string]*PoolDiagnostic)
	for _, rec := range h.targets {
		pd, ok := byPool[rec.PoolID]
		if !ok {
			pd = &PoolDiagnostic{PoolID: rec.PoolID}
			byPool[rec.PoolID] = pd
		}
		pd.TotalTargets++
		effStatus := rec.Status
		if !rec.Enabled {
			effStatus = StatusDisabled
			pd.DisabledTargets++
		} else if rec.Status == StatusUnhealthy && (rec.PassiveOpenUntil.IsZero() || h.now().Before(rec.PassiveOpenUntil)) {
			pd.UnhealthyTargets++
		} else {
			effStatus = StatusHealthy
			pd.HealthyTargets++
		}
		if includeTargets {
			var lastProbe *time.Time
			if !rec.LastProbeAt.IsZero() {
				t := rec.LastProbeAt
				lastProbe = &t
			}
			pd.Targets = append(pd.Targets, TargetDiagnostic{
				TargetID:            rec.TargetID,
				Status:              effStatus,
				Enabled:             rec.Enabled,
				ConsecutiveFailures: rec.ConsecutiveFailures,
				LastProbeAt:         lastProbe,
			})
		}
	}
	h.mu.RUnlock()

	out := make([]PoolDiagnostic, 0, len(byPool))
	for _, pd := range byPool {
		switch {
		case pd.HealthyTargets == 0:
			pd.Status = "unavailable"
		case pd.UnhealthyTargets > 0:
			pd.Status = "degraded"
		default:
			pd.Status = "healthy"
		}
		if len(pd.Targets) > 1 {
			sort.Slice(pd.Targets, func(i, j int) bool {
				return pd.Targets[i].TargetID < pd.Targets[j].TargetID
			})
		}
		out = append(out, *pd)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PoolID < out[j].PoolID })
	return out
}
