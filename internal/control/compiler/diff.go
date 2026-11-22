package compiler

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/YASSERRMD/GatewayForge/internal/shared/snapshot"
)

// DiffEntry describes one semantic structural change between an active snapshot and a candidate snapshot.
// Secrets and credential hashes are NEVER included.
type DiffEntry struct {
	ResourceType string   `json:"resource_type"` // route | upstream_pool | policy | consumer
	ResourceID   string   `json:"resource_id"`
	Name         string   `json:"name"`
	ChangeType   string   `json:"change_type"` // added | removed | modified
	Details      []string `json:"details"`
}

// DiffSummary holds counts of added, modified, and removed resources.
type DiffSummary struct {
	Added    int `json:"added"`
	Modified int `json:"modified"`
	Removed  int `json:"removed"`
}

// ConfigDiff is the deterministic structural diff between base (active) and next (draft) snapshots.
type ConfigDiff struct {
	BaseVersion int         `json:"base_version"`
	HasChanges  bool        `json:"has_changes"`
	Summary     DiffSummary `json:"summary"`
	Changes     []DiffEntry `json:"changes"`
}

// DiffSnapshots computes a stable semantic diff of routes, upstream pools, attached policies,
// and consumer metadata without exposing any key hashes or credentials.
func DiffSnapshots(base *snapshot.Snapshot, next snapshot.Snapshot) ConfigDiff {
	diff := ConfigDiff{
		Changes: make([]DiffEntry, 0),
	}
	var basePools []snapshot.UpstreamPool
	var baseRoutes []snapshot.Route
	var baseConsumers []snapshot.Consumer
	if base != nil {
		diff.BaseVersion = base.Version
		basePools = base.Pools
		baseRoutes = base.Routes
		baseConsumers = base.Consumers
	}

	diffPools(basePools, next.Pools, &diff.Changes)
	diffRoutes(baseRoutes, next.Routes, &diff.Changes)
	diffConsumers(baseConsumers, next.Consumers, &diff.Changes)

	sort.Slice(diff.Changes, func(i, j int) bool {
		if diff.Changes[i].ResourceType != diff.Changes[j].ResourceType {
			return diff.Changes[i].ResourceType < diff.Changes[j].ResourceType
		}
		if diff.Changes[i].ChangeType != diff.Changes[j].ChangeType {
			return diff.Changes[i].ChangeType < diff.Changes[j].ChangeType
		}
		if diff.Changes[i].Name != diff.Changes[j].Name {
			return diff.Changes[i].Name < diff.Changes[j].Name
		}
		return diff.Changes[i].ResourceID < diff.Changes[j].ResourceID
	})

	for _, c := range diff.Changes {
		switch c.ChangeType {
		case "added":
			diff.Summary.Added++
		case "modified":
			diff.Summary.Modified++
		case "removed":
			diff.Summary.Removed++
		}
	}
	diff.HasChanges = len(diff.Changes) > 0
	return diff
}

func diffPools(base, next []snapshot.UpstreamPool, out *[]DiffEntry) {
	baseMap := make(map[string]snapshot.UpstreamPool, len(base))
	for _, p := range base {
		baseMap[p.ID] = p
	}
	nextMap := make(map[string]snapshot.UpstreamPool, len(next))
	for _, p := range next {
		nextMap[p.ID] = p
	}

	for id, np := range nextMap {
		bp, existed := baseMap[id]
		if !existed {
			*out = append(*out, DiffEntry{
				ResourceType: "upstream_pool",
				ResourceID:   np.ID,
				Name:         np.Name,
				ChangeType:   "added",
				Details:      []string{fmt.Sprintf("class=%s, %d target(s)", np.DestinationClass, len(np.Targets))},
			})
			continue
		}
		var details []string
		if bp.Name != np.Name {
			details = append(details, fmt.Sprintf("name: %q -> %q", bp.Name, np.Name))
		}
		if bp.DestinationClass != np.DestinationClass {
			details = append(details, fmt.Sprintf("destination_class: %s -> %s", bp.DestinationClass, np.DestinationClass))
		}
		if bp.ConnectTimeoutMS != np.ConnectTimeoutMS || bp.ReadTimeoutMS != np.ReadTimeoutMS {
			details = append(details, fmt.Sprintf("timeouts: (%dms/%dms) -> (%dms/%dms)", bp.ConnectTimeoutMS, bp.ReadTimeoutMS, np.ConnectTimeoutMS, np.ReadTimeoutMS))
		}
		if !reflect.DeepEqual(bp.Targets, np.Targets) {
			details = append(details, fmt.Sprintf("targets changed (%d -> %d)", len(bp.Targets), len(np.Targets)))
		}
		if len(details) > 0 {
			*out = append(*out, DiffEntry{
				ResourceType: "upstream_pool",
				ResourceID:   np.ID,
				Name:         np.Name,
				ChangeType:   "modified",
				Details:      details,
			})
		}
	}

	for id, bp := range baseMap {
		if _, exists := nextMap[id]; !exists {
			*out = append(*out, DiffEntry{
				ResourceType: "upstream_pool",
				ResourceID:   bp.ID,
				Name:         bp.Name,
				ChangeType:   "removed",
				Details:      []string{fmt.Sprintf("removed pool with %d target(s)", len(bp.Targets))},
			})
		}
	}
}

func diffRoutes(base, next []snapshot.Route, out *[]DiffEntry) {
	baseMap := make(map[string]snapshot.Route, len(base))
	for _, r := range base {
		baseMap[r.ID] = r
	}
	nextMap := make(map[string]snapshot.Route, len(next))
	for _, r := range next {
		nextMap[r.ID] = r
	}

	for id, nr := range nextMap {
		br, existed := baseMap[id]
		if !existed {
			*out = append(*out, DiffEntry{
				ResourceType: "route",
				ResourceID:   nr.ID,
				Name:         nr.Name,
				ChangeType:   "added",
				Details:      []string{fmt.Sprintf("%s %s%s (priority %d, %d policies)", strings.Join(nr.Methods, ","), nr.Host, nr.PathPrefix, nr.Priority, len(nr.Policies))},
			})
			continue
		}
		var details []string
		if br.Host != nr.Host || br.PathPrefix != nr.PathPrefix {
			details = append(details, fmt.Sprintf("matcher: %s%s -> %s%s", br.Host, br.PathPrefix, nr.Host, nr.PathPrefix))
		}
		if !reflect.DeepEqual(br.Methods, nr.Methods) {
			details = append(details, fmt.Sprintf("methods: [%s] -> [%s]", strings.Join(br.Methods, ","), strings.Join(nr.Methods, ",")))
		}
		if br.Priority != nr.Priority {
			details = append(details, fmt.Sprintf("priority: %d -> %d", br.Priority, nr.Priority))
		}
		if br.UpstreamPoolID != nr.UpstreamPoolID {
			details = append(details, "upstream_pool changed")
		}
		if br.StripPrefix != nr.StripPrefix {
			details = append(details, fmt.Sprintf("strip_prefix: %v -> %v", br.StripPrefix, nr.StripPrefix))
		}
		if !policiesEqual(br.Policies, nr.Policies) {
			details = append(details, fmt.Sprintf("attached policies changed (%d -> %d)", len(br.Policies), len(nr.Policies)))
		}
		if len(details) > 0 {
			*out = append(*out, DiffEntry{
				ResourceType: "route",
				ResourceID:   nr.ID,
				Name:         nr.Name,
				ChangeType:   "modified",
				Details:      details,
			})
		}
	}

	for id, br := range baseMap {
		if _, exists := nextMap[id]; !exists {
			*out = append(*out, DiffEntry{
				ResourceType: "route",
				ResourceID:   br.ID,
				Name:         br.Name,
				ChangeType:   "removed",
				Details:      []string{fmt.Sprintf("removed %s %s%s", strings.Join(br.Methods, ","), br.Host, br.PathPrefix)},
			})
		}
	}
}

func policiesEqual(a, b []snapshot.AttachedPolicy) bool {
	if len(a) != len(b) {
		return false
	}
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(ab) == string(bb)
}

func diffConsumers(base, next []snapshot.Consumer, out *[]DiffEntry) {
	baseMap := make(map[string]snapshot.Consumer, len(base))
	for _, c := range base {
		baseMap[c.ID] = c
	}
	nextMap := make(map[string]snapshot.Consumer, len(next))
	for _, c := range next {
		nextMap[c.ID] = c
	}

	for id, nc := range nextMap {
		bc, existed := baseMap[id]
		if !existed {
			*out = append(*out, DiffEntry{
				ResourceType: "consumer",
				ResourceID:   nc.ID,
				Name:         nc.Name,
				ChangeType:   "added",
				Details:      []string{fmt.Sprintf("status=%s, %d active key(s)", nc.Status, len(nc.Keys))},
			})
			continue
		}
		var details []string
		if bc.Status != nc.Status {
			details = append(details, fmt.Sprintf("status: %s -> %s", bc.Status, nc.Status))
		}
		if len(bc.Keys) != len(nc.Keys) {
			details = append(details, fmt.Sprintf("active key count: %d -> %d", len(bc.Keys), len(nc.Keys)))
		}
		if len(details) > 0 {
			*out = append(*out, DiffEntry{
				ResourceType: "consumer",
				ResourceID:   nc.ID,
				Name:         nc.Name,
				ChangeType:   "modified",
				Details:      details,
			})
		}
	}

	for id, bc := range baseMap {
		if _, exists := nextMap[id]; !exists {
			*out = append(*out, DiffEntry{
				ResourceType: "consumer",
				ResourceID:   bc.ID,
				Name:         bc.Name,
				ChangeType:   "removed",
				Details:      []string{"consumer removed"},
			})
		}
	}
}
