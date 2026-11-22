package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	CorrelationKeyPrefix  = "gwf:corr:"
	DefaultCorrelationTTL = time.Hour
)

var safeRequestIDRegex = regexp.MustCompile(`^[A-Za-z0-9_.-]{4,128}$`)
var safeTraceIDRegex = regexp.MustCompile(`^[0-9a-f]{32}$`)

// RequestCorrelation is a secret-free diagnostic link record between a gateway request_id and its W3C trace_id.
type RequestCorrelation struct {
	RequestID  string    `json:"request_id"`
	TraceID    string    `json:"trace_id"`
	OrgID      string    `json:"org_id"`
	RouteID    string    `json:"route_id"`
	ServiceID  string    `json:"service_id"`
	Method     string    `json:"method"`
	Status     int       `json:"status"`
	Outcome    string    `json:"outcome"`
	DurationMS float64   `json:"duration_ms"`
	Timestamp  time.Time `json:"timestamp"`
}

// TraceLinkResponse is returned by GET /orgs/{orgId}/telemetry/traces/{requestId}.
type TraceLinkResponse struct {
	Found                bool                `json:"found"`
	TraceViewerAvailable bool                `json:"trace_viewer_available"`
	TraceURL             string              `json:"trace_url,omitempty"`
	UnavailableReason    string              `json:"unavailable_reason,omitempty"`
	Record               *RequestCorrelation `json:"record,omitempty"`
}

// CorrelationStore persists tenant-scoped request->trace lookup records in Redis with short bounded TTL.
type CorrelationStore struct {
	client redis.Cmdable
	prefix string
	ttl    time.Duration
}

// NewCorrelationStore creates a CorrelationStore backed by Redis.
func NewCorrelationStore(client redis.Cmdable, prefix string, ttl time.Duration) *CorrelationStore {
	if strings.TrimSpace(prefix) == "" {
		prefix = CorrelationKeyPrefix
	}
	if ttl <= 0 {
		ttl = DefaultCorrelationTTL
	}
	return &CorrelationStore{client: client, prefix: prefix, ttl: ttl}
}

func (s *CorrelationStore) key(orgID, requestID string) string {
	return fmt.Sprintf("%s%s:%s", s.prefix, orgID, requestID)
}

// Record stores one safe request->trace correlation entry.
func (s *CorrelationStore) Record(ctx context.Context, rec RequestCorrelation) error {
	if s == nil || s.client == nil || rec.OrgID == "" || !safeRequestIDRegex.MatchString(rec.RequestID) {
		return nil
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return s.client.Set(ctx, s.key(rec.OrgID, rec.RequestID), raw, s.ttl).Err()
}

// Lookup retrieves a tenant-scoped request correlation entry by (orgID, requestID).
func (s *CorrelationStore) Lookup(ctx context.Context, orgID, requestID string) (*RequestCorrelation, error) {
	if s == nil || s.client == nil {
		return nil, errors.New("correlation store unavailable")
	}
	if !safeRequestIDRegex.MatchString(requestID) {
		return nil, errors.New("invalid request_id format")
	}
	raw, err := s.client.Get(ctx, s.key(orgID, requestID)).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var rec RequestCorrelation
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		return nil, err
	}
	if rec.OrgID != orgID {
		return nil, nil
	}
	return &rec, nil
}

// BuildTraceViewerURL safely constructs an external trace viewer link (e.g., Jaeger) if baseURL and traceID are valid.
func BuildTraceViewerURL(baseURL, traceID string) (string, bool) {
	base := strings.TrimSpace(baseURL)
	if base == "" || !safeTraceIDRegex.MatchString(traceID) {
		return "", false
	}
	parsed, err := url.Parse(base)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", false
	}
	return strings.TrimRight(base, "/") + "/" + traceID, true
}

// IsSafeRequestID reports whether id is a valid request identifier.
func IsSafeRequestID(id string) bool {
	return safeRequestIDRegex.MatchString(id)
}
