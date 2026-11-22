package table

import (
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/YASSERRMD/GatewayForge/internal/gateway/matcher"
	"github.com/YASSERRMD/GatewayForge/internal/shared/snapshot"
)

// ConsumerKeyRef binds a compiled API-key record to its owning consumer for O(1) lookup.
type ConsumerKeyRef struct {
	ConsumerID     string
	ConsumerName   string
	ConsumerStatus string
	Key            snapshot.APIKey
}

// CompiledTable is a completely immutable routing & policy snapshot used for the lifetime of a request.
type CompiledTable struct {
	OrgID      string
	Version    int
	Checksum   string
	LoadedAt   time.Time
	Matcher    *matcher.Matcher
	Pools      map[string]snapshot.UpstreamPool
	KeysByPref map[string]ConsumerKeyRef
	Raw        snapshot.Snapshot
}

// Compile validates and transforms a snapshot into an immutable CompiledTable.
func Compile(snap snapshot.Snapshot, verifyChecksum bool) (*CompiledTable, error) {
	if snap.SchemaVersion != snapshot.SchemaVersion {
		return nil, fmt.Errorf("unsupported snapshot schema_version %d (want %d)", snap.SchemaVersion, snapshot.SchemaVersion)
	}
	if verifyChecksum {
		if err := snap.VerifyChecksum(); err != nil {
			return nil, err
		}
	}

	pools := make(map[string]snapshot.UpstreamPool, len(snap.Pools))
	for _, p := range snap.Pools {
		if p.ID == "" {
			return nil, errors.New("upstream pool missing id")
		}
		cp := p
		cp.Targets = append([]snapshot.UpstreamTarget(nil), p.Targets...)
		pools[p.ID] = cp
	}

	routesCopy := make([]snapshot.Route, len(snap.Routes))
	for i, r := range snap.Routes {
		if r.Enabled {
			if _, ok := pools[r.UpstreamPoolID]; !ok {
				return nil, fmt.Errorf("route %q references missing upstream pool %q", r.ID, r.UpstreamPoolID)
			}
		}
		rc := r
		rc.Methods = append([]string(nil), r.Methods...)
		rc.Policies = append([]snapshot.AttachedPolicy(nil), r.Policies...)
		routesCopy[i] = rc
	}

	keysByPref := make(map[string]ConsumerKeyRef)
	for _, c := range snap.Consumers {
		for _, k := range c.Keys {
			if k.KeyPrefix == "" || k.KeyHash == "" {
				continue
			}
			keysByPref[k.KeyPrefix] = ConsumerKeyRef{
				ConsumerID:     c.ID,
				ConsumerName:   c.Name,
				ConsumerStatus: c.Status,
				Key:            k,
			}
		}
	}

	checksum := snap.Checksum
	if checksum == "" {
		if computed, err := snap.ComputeChecksum(); err == nil {
			checksum = computed
		}
	}

	return &CompiledTable{
		OrgID:      snap.OrgID,
		Version:    snap.Version,
		Checksum:   checksum,
		LoadedAt:   time.Now().UTC(),
		Matcher:    matcher.Compile(routesCopy),
		Pools:      pools,
		KeysByPref: keysByPref,
		Raw:        snap,
	}, nil
}

// Holder manages atomic pointer swaps of immutable CompiledTable instances.
type Holder struct {
	current   atomic.Pointer[CompiledTable]
	lastError atomic.Pointer[string]
}

// NewHolder initializes a Holder with an empty deny-by-default routing table.
func NewHolder() *Holder {
	h := &Holder{}
	empty := &CompiledTable{
		Version:    0,
		LoadedAt:   time.Now().UTC(),
		Matcher:    matcher.Compile(nil),
		Pools:      map[string]snapshot.UpstreamPool{},
		KeysByPref: map[string]ConsumerKeyRef{},
	}
	h.current.Store(empty)
	return h
}

// Current returns the currently active immutable table pointer lock-free.
func (h *Holder) Current() *CompiledTable {
	return h.current.Load()
}

// LastError returns the most recent snapshot compilation/swap error, if any.
func (h *Holder) LastError() string {
	if p := h.lastError.Load(); p != nil {
		return *p
	}
	return ""
}

// Apply compiles snap and atomically replaces the active table on success.
// If compilation fails, the previous last-known-good table remains active untouched.
func (h *Holder) Apply(snap snapshot.Snapshot, verifyChecksum bool) (*CompiledTable, error) {
	compiled, err := Compile(snap, verifyChecksum)
	if err != nil {
		msg := err.Error()
		h.lastError.Store(&msg)
		return nil, err
	}
	h.current.Store(compiled)
	empty := ""
	h.lastError.Store(&empty)
	return compiled, nil
}
