package snapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// SchemaVersion is the current immutable data-plane snapshot contract version.
const SchemaVersion = 1

// Snapshot is the versioned, checksummed configuration document consumed read-only by gateways.
type Snapshot struct {
	SchemaVersion int            `json:"schema_version"`
	OrgID         string         `json:"org_id"`
	Version       int            `json:"version"`
	Checksum      string         `json:"checksum,omitempty"`
	PublishedAt   time.Time      `json:"published_at"`
	Pools         []UpstreamPool `json:"pools"`
	Routes        []Route        `json:"routes"`
	Consumers     []Consumer     `json:"consumers,omitempty"`
}

// UpstreamPool defines an approved backend target pool and its timeouts/health probes.
type UpstreamPool struct {
	ID                    string           `json:"id"`
	Name                  string           `json:"name"`
	DestinationClass      string           `json:"destination_class"`
	ConnectTimeoutMS      int              `json:"connect_timeout_ms"`
	ReadTimeoutMS         int              `json:"read_timeout_ms"`
	HealthCheckPath       string           `json:"health_check_path"`
	HealthCheckIntervalMS int              `json:"health_check_interval_ms"`
	HealthCheckTimeoutMS  int              `json:"health_check_timeout_ms"`
	Targets               []UpstreamTarget `json:"targets"`
}

// UpstreamTarget is a single weighted destination inside a pool.
type UpstreamTarget struct {
	ID      string `json:"id"`
	URL     string `json:"url"`
	Weight  int    `json:"weight"`
	Enabled bool   `json:"enabled"`
}

// Route defines explicit host/path/method matching and attached policies.
type Route struct {
	ID               string           `json:"id"`
	OrgID            string           `json:"org_id"`
	ServiceID        string           `json:"service_id"`
	ServiceSlug      string           `json:"service_slug"`
	UpstreamPoolID   string           `json:"upstream_pool_id"`
	Name             string           `json:"name"`
	Host             string           `json:"host"`
	PathPrefix       string           `json:"path_prefix"`
	Methods          []string         `json:"methods"`
	StripPrefix      bool             `json:"strip_prefix"`
	Priority         int              `json:"priority"`
	Enabled          bool             `json:"enabled"`
	MaxBodyBytes     int64            `json:"max_body_bytes,omitempty"`
	RequestTimeoutMS int              `json:"request_timeout_ms,omitempty"`
	Policies         []AttachedPolicy `json:"policies,omitempty"`
}

// AttachedPolicy represents a resolved policy attached to a route (from org, service, or route scope).
type AttachedPolicy struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	PolicyType    string          `json:"policy_type"`
	SchemaVersion int             `json:"schema_version"`
	ScopeType     string          `json:"scope_type"`
	Priority      int             `json:"priority"`
	Config        json.RawMessage `json:"config"`
}

// Consumer represents a compiled consumer and its active hashed API keys (NEVER plaintext).
type Consumer struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Status string   `json:"status"`
	Keys   []APIKey `json:"keys"`
}

// APIKey holds only the prefix, keyed hash, scopes, and expiry metadata for gateway verification.
type APIKey struct {
	ID        string     `json:"id"`
	KeyPrefix string     `json:"key_prefix"`
	KeyHash   string     `json:"key_hash"`
	Scopes    []string   `json:"scopes"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// ComputeChecksum computes the canonical SHA-256 hex digest of the snapshot excluding the Checksum field.
func (s Snapshot) ComputeChecksum() (string, error) {
	clone := s
	clone.Checksum = ""
	raw, err := json.Marshal(clone)
	if err != nil {
		return "", fmt.Errorf("marshal snapshot for checksum: %w", err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// VerifyChecksum returns an error if Checksum is missing or does not match the canonical digest.
func (s Snapshot) VerifyChecksum() error {
	if s.Checksum == "" {
		return fmt.Errorf("snapshot checksum is empty")
	}
	expected, err := s.ComputeChecksum()
	if err != nil {
		return err
	}
	if s.Checksum != expected {
		return fmt.Errorf("snapshot checksum mismatch: expected %s got %s", expected, s.Checksum)
	}
	return nil
}
