package telemetry

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	HeartbeatKeyPrefix = "gwf:gw:hb:"
	DefaultHBInterval  = 10 * time.Second
	DefaultHBTTL       = 90 * time.Second
)

// GatewayHeartbeat is the minimal, secret-free liveness and convergence record emitted by each gateway replica.
type GatewayHeartbeat struct {
	InstanceID    string    `json:"instance_id"`
	OrgID         string    `json:"org_id"`
	ActiveVersion int       `json:"active_version"`
	Checksum      string    `json:"checksum"`
	Ready         bool      `json:"ready"`
	BuildVersion  string    `json:"build_version,omitempty"`
	LastSeenAt    time.Time `json:"last_seen_at"`
}

// GatewayInstanceStatus enriches a heartbeat with derived liveness and convergence state.
type GatewayInstanceStatus struct {
	GatewayHeartbeat
	Status    string `json:"status"`    // healthy | stale | offline
	Converged bool   `json:"converged"` // true when active_version & checksum match target
	AgeSec    int64  `json:"age_sec"`
}

// ClassifyHeartbeat maps heartbeat age into healthy / stale / offline without treating a brief gap as instant outage.
func ClassifyHeartbeat(now, lastSeen time.Time, staleAfter, offlineAfter time.Duration) (status string, ageSec int64) {
	if staleAfter <= 0 {
		staleAfter = 30 * time.Second
	}
	if offlineAfter <= staleAfter {
		offlineAfter = staleAfter * 2
	}
	age := now.Sub(lastSeen)
	if age < 0 {
		age = 0
	}
	ageSec = int64(age.Seconds())
	switch {
	case age <= staleAfter:
		return "healthy", ageSec
	case age <= offlineAfter:
		return "stale", ageSec
	default:
		return "offline", ageSec
	}
}

// HeartbeatStore reads and writes gateway instance heartbeats in Redis with bounded TTL expiry.
type HeartbeatStore struct {
	client       redis.Cmdable
	keyPrefix    string
	ttl          time.Duration
	staleAfter   time.Duration
	offlineAfter time.Duration
	now          func() time.Time
}

// NewHeartbeatStore constructs a Redis-backed HeartbeatStore.
func NewHeartbeatStore(client redis.Cmdable, prefix string) *HeartbeatStore {
	if strings.TrimSpace(prefix) == "" {
		prefix = HeartbeatKeyPrefix
	}
	return &HeartbeatStore{
		client:       client,
		keyPrefix:    prefix,
		ttl:          DefaultHBTTL,
		staleAfter:   30 * time.Second,
		offlineAfter: 60 * time.Second,
		now:          func() time.Time { return time.Now().UTC() },
	}
}

// Publish stores one gateway replica heartbeat with bounded TTL.
func (s *HeartbeatStore) Publish(ctx context.Context, hb GatewayHeartbeat) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("heartbeat redis client unavailable")
	}
	if strings.TrimSpace(hb.InstanceID) == "" {
		return fmt.Errorf("instance_id is required")
	}
	if hb.LastSeenAt.IsZero() {
		hb.LastSeenAt = s.now()
	}
	raw, err := json.Marshal(hb)
	if err != nil {
		return err
	}
	key := s.keyPrefix + hb.InstanceID
	return s.client.Set(ctx, key, raw, s.ttl).Err()
}

// ListForOrg returns all known gateway replicas relevant to orgID (matching orgID or unassigned bootstrapping replicas)
// with liveness classification and convergence compared to activeVersion/activeChecksum.
func (s *HeartbeatStore) ListForOrg(ctx context.Context, orgID string, activeVersion int, activeChecksum string) ([]GatewayInstanceStatus, error) {
	if s == nil || s.client == nil {
		return nil, fmt.Errorf("heartbeat redis client unavailable")
	}
	now := s.now()
	var keys []string
	var cursor uint64
	for {
		batch, next, err := s.client.Scan(ctx, cursor, s.keyPrefix+"*", 100).Result()
		if err != nil {
			return nil, err
		}
		keys = append(keys, batch...)
		cursor = next
		if cursor == 0 {
			break
		}
	}
	if len(keys) == 0 {
		return []GatewayInstanceStatus{}, nil
	}

	vals, err := s.client.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}

	out := make([]GatewayInstanceStatus, 0, len(vals))
	for _, v := range vals {
		str, ok := v.(string)
		if !ok || str == "" {
			continue
		}
		var hb GatewayHeartbeat
		if err := json.Unmarshal([]byte(str), &hb); err != nil {
			continue
		}
		if hb.OrgID != "" && hb.OrgID != orgID {
			continue
		}
		st, ageSec := ClassifyHeartbeat(now, hb.LastSeenAt, s.staleAfter, s.offlineAfter)
		converged := activeVersion > 0 && hb.ActiveVersion == activeVersion && (activeChecksum == "" || hb.Checksum == activeChecksum)
		out = append(out, GatewayInstanceStatus{
			GatewayHeartbeat: hb,
			Status:           st,
			Converged:        converged,
			AgeSec:           ageSec,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].InstanceID < out[j].InstanceID })
	return out, nil
}
