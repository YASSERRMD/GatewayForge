package telemetry

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	AggregateKeyPrefix = "gwf:agg:"
	DefaultRetention   = 24 * time.Hour
)

// TrafficSample is one completed gateway request observation written into a tenant-isolated minute rollup.
type TrafficSample struct {
	Timestamp  time.Time
	OrgID      string
	RouteID    string
	ServiceID  string
	Status     int
	Outcome    string
	DurationMS float64
}

// MetricsSummary aggregates authentic gateway traffic metrics for an organization over a time range.
type MetricsSummary struct {
	OrgID          string  `json:"org_id"`
	Range          string  `json:"range"`
	WindowSeconds  int64   `json:"window_seconds"`
	TotalRequests  int64   `json:"total_requests"`
	RPS            float64 `json:"rps"`
	Errors4xx      int64   `json:"errors_4xx"`
	Errors5xx      int64   `json:"errors_5xx"`
	AuthDenied     int64   `json:"auth_denied"`
	RateLimited    int64   `json:"rate_limited"`
	ErrorRate      float64 `json:"error_rate"`
	AvgLatencyMS   float64 `json:"avg_latency_ms"`
	P50LatencyMS   float64 `json:"p50_latency_ms"`
	P95LatencyMS   float64 `json:"p95_latency_ms"`
	P99LatencyMS   float64 `json:"p99_latency_ms"`
	ActiveGateways int     `json:"active_gateways"`
	StaleGateways  int     `json:"stale_gateways"`
	DataAvailable  bool    `json:"data_available"`
}

// TimeSeriesPoint is one aggregated bucket in a traffic trend series.
type TimeSeriesPoint struct {
	Timestamp    time.Time `json:"timestamp"`
	Requests     int64     `json:"requests"`
	Errors4xx    int64     `json:"errors_4xx"`
	Errors5xx    int64     `json:"errors_5xx"`
	AuthDenied   int64     `json:"auth_denied"`
	RateLimited  int64     `json:"rate_limited"`
	AvgLatencyMS float64   `json:"avg_latency_ms"`
	P95LatencyMS float64   `json:"p95_latency_ms"`
}

// ErrorBreakdown summarizes grouped error counts by status class and outcome.
type ErrorBreakdown struct {
	OrgID         string           `json:"org_id"`
	Range         string           `json:"range"`
	TotalRequests int64            `json:"total_requests"`
	TotalErrors   int64            `json:"total_errors"`
	ByStatusClass map[string]int64 `json:"by_status_class"`
	ByOutcome     map[string]int64 `json:"by_outcome"`
	DataAvailable bool             `json:"data_available"`
}

var latencyUpperBounds = []float64{10, 50, 100, 250, 500, 1000, 5000}
var latencyFieldNames = []string{"lat_10", "lat_50", "lat_100", "lat_250", "lat_500", "lat_1000", "lat_5000", "lat_inf"}

// AggregateStore persists and queries tenant-isolated minute-bucket traffic rollups in Redis with bounded retention.
type AggregateStore struct {
	client    redis.Cmdable
	prefix    string
	retention time.Duration
	now       func() time.Time
}

// NewAggregateStore constructs a tenant-isolated Redis rollup store with configurable retention.
func NewAggregateStore(client redis.Cmdable, prefix string, retention time.Duration) *AggregateStore {
	if strings.TrimSpace(prefix) == "" {
		prefix = AggregateKeyPrefix
	}
	if retention <= 0 {
		retention = DefaultRetention
	}
	return &AggregateStore{
		client:    client,
		prefix:    prefix,
		retention: retention,
		now:       func() time.Time { return time.Now().UTC() },
	}
}

func (s *AggregateStore) minuteKey(orgID string, t time.Time) string {
	return fmt.Sprintf("%s%s:%s", s.prefix, orgID, t.UTC().Format("200601021504"))
}

// Record increments tenant-isolated minute rollup counters for one completed request.
func (s *AggregateStore) Record(ctx context.Context, sample TrafficSample) error {
	if s == nil || s.client == nil || strings.TrimSpace(sample.OrgID) == "" {
		return nil
	}
	ts := sample.Timestamp
	if ts.IsZero() {
		ts = s.now()
	}
	key := s.minuteKey(sample.OrgID, ts)
	durMicro := int64(math.Round(sample.DurationMS * 1000))
	if durMicro < 0 {
		durMicro = 0
	}

	pipe := s.client.Pipeline()
	pipe.HIncrBy(ctx, key, "requests", 1)
	pipe.HIncrBy(ctx, key, "dur_us_sum", durMicro)
	switch {
	case sample.Status >= 200 && sample.Status < 300:
		pipe.HIncrBy(ctx, key, "status_2xx", 1)
	case sample.Status >= 300 && sample.Status < 400:
		pipe.HIncrBy(ctx, key, "status_3xx", 1)
	case sample.Status >= 400 && sample.Status < 500:
		pipe.HIncrBy(ctx, key, "errors_4xx", 1)
	case sample.Status >= 500:
		pipe.HIncrBy(ctx, key, "errors_5xx", 1)
	}
	if sample.Status == 401 || sample.Status == 403 || sample.Outcome == "auth_denied" {
		pipe.HIncrBy(ctx, key, "auth_denied", 1)
	}
	if sample.Status == 429 || sample.Outcome == "rate_limited" {
		pipe.HIncrBy(ctx, key, "rate_limited", 1)
	}
	if sample.Outcome != "" {
		pipe.HIncrBy(ctx, key, "out_"+sample.Outcome, 1)
	}

	latField := latencyFieldNames[len(latencyFieldNames)-1]
	for i, ub := range latencyUpperBounds {
		if sample.DurationMS <= ub {
			latField = latencyFieldNames[i]
			break
		}
	}
	pipe.HIncrBy(ctx, key, latField, 1)
	pipe.Expire(ctx, key, s.retention)
	_, err := pipe.Exec(ctx)
	return err
}

// ParseRange resolves a dashboard timeframe parameter ("15m", "1h", "6h", "24h") into canonical label,
// total window duration, and sane bucket step minutes.
func ParseRange(raw string) (label string, total time.Duration, bucketMinutes int) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "15m":
		return "15m", 15 * time.Minute, 1
	case "6h":
		return "6h", 6 * time.Hour, 15
	case "24h":
		return "24h", 24 * time.Hour, 60
	default:
		return "1h", time.Hour, 5
	}
}

type rawBucket struct {
	ts     time.Time
	fields map[string]int64
}

func (s *AggregateStore) loadMinuteBuckets(ctx context.Context, orgID string, window time.Duration) ([]rawBucket, error) {
	if s == nil || s.client == nil {
		return nil, fmt.Errorf("aggregate store redis client unavailable")
	}
	minutes := int(window / time.Minute)
	if minutes < 1 {
		minutes = 1
	}
	endMin := s.now().UTC().Truncate(time.Minute)
	startMin := endMin.Add(-time.Duration(minutes-1) * time.Minute)

	pipe := s.client.Pipeline()
	cmds := make([]*redis.MapStringStringCmd, minutes)
	times := make([]time.Time, minutes)
	for i := 0; i < minutes; i++ {
		ts := startMin.Add(time.Duration(i) * time.Minute)
		times[i] = ts
		cmds[i] = pipe.HGetAll(ctx, s.minuteKey(orgID, ts))
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, err
	}

	out := make([]rawBucket, minutes)
	for i := 0; i < minutes; i++ {
		m := make(map[string]int64)
		for k, v := range cmds[i].Val() {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				m[k] = n
			}
		}
		out[i] = rawBucket{ts: times[i], fields: m}
	}
	return out, nil
}

// Summary computes aggregated traffic, error, and latency statistics strictly scoped to orgID.
func (s *AggregateStore) Summary(ctx context.Context, orgID, rangeParam string) (MetricsSummary, error) {
	label, window, _ := ParseRange(rangeParam)
	res := MetricsSummary{
		OrgID:         orgID,
		Range:         label,
		WindowSeconds: int64(window.Seconds()),
	}
	buckets, err := s.loadMinuteBuckets(ctx, orgID, window)
	if err != nil {
		return res, err
	}
	res.DataAvailable = true

	var durUsSum int64
	latCounts := make([]int64, len(latencyFieldNames))
	for _, b := range buckets {
		res.TotalRequests += b.fields["requests"]
		res.Errors4xx += b.fields["errors_4xx"]
		res.Errors5xx += b.fields["errors_5xx"]
		res.AuthDenied += b.fields["auth_denied"]
		res.RateLimited += b.fields["rate_limited"]
		durUsSum += b.fields["dur_us_sum"]
		for i, fn := range latencyFieldNames {
			latCounts[i] += b.fields[fn]
		}
	}

	if res.TotalRequests > 0 {
		res.RPS = math.Round((float64(res.TotalRequests)/window.Seconds())*100) / 100
		res.ErrorRate = math.Round((float64(res.Errors4xx+res.Errors5xx)/float64(res.TotalRequests))*10000) / 10000
		res.AvgLatencyMS = math.Round((float64(durUsSum)/1000.0/float64(res.TotalRequests))*100) / 100
		res.P50LatencyMS = estimatePercentileMS(latCounts, 0.50)
		res.P95LatencyMS = estimatePercentileMS(latCounts, 0.95)
		res.P99LatencyMS = estimatePercentileMS(latCounts, 0.99)
	}
	return res, nil
}

// TimeSeries returns grouped time-series points across rangeParam using sane bucket selection.
func (s *AggregateStore) TimeSeries(ctx context.Context, orgID, rangeParam string) (label string, bucketMinutes int, points []TimeSeriesPoint, err error) {
	label, window, bucketMinutes := ParseRange(rangeParam)
	buckets, err := s.loadMinuteBuckets(ctx, orgID, window)
	if err != nil {
		return label, bucketMinutes, nil, err
	}

	points = make([]TimeSeriesPoint, 0, len(buckets)/bucketMinutes+1)
	for i := 0; i < len(buckets); i += bucketMinutes {
		end := i + bucketMinutes
		if end > len(buckets) {
			end = len(buckets)
		}
		pt := TimeSeriesPoint{Timestamp: buckets[i].ts}
		var durUs int64
		latCounts := make([]int64, len(latencyFieldNames))
		for j := i; j < end; j++ {
			f := buckets[j].fields
			pt.Requests += f["requests"]
			pt.Errors4xx += f["errors_4xx"]
			pt.Errors5xx += f["errors_5xx"]
			pt.AuthDenied += f["auth_denied"]
			pt.RateLimited += f["rate_limited"]
			durUs += f["dur_us_sum"]
			for k, fn := range latencyFieldNames {
				latCounts[k] += f[fn]
			}
		}
		if pt.Requests > 0 {
			pt.AvgLatencyMS = math.Round((float64(durUs)/1000.0/float64(pt.Requests))*100) / 100
			pt.P95LatencyMS = estimatePercentileMS(latCounts, 0.95)
		}
		points = append(points, pt)
	}
	return label, bucketMinutes, points, nil
}

// Errors returns grouped error status-class and outcome breakdown for orgID over rangeParam.
func (s *AggregateStore) Errors(ctx context.Context, orgID, rangeParam string) (ErrorBreakdown, error) {
	label, window, _ := ParseRange(rangeParam)
	out := ErrorBreakdown{
		OrgID:         orgID,
		Range:         label,
		ByStatusClass: map[string]int64{"2xx": 0, "3xx": 0, "4xx": 0, "5xx": 0},
		ByOutcome:     map[string]int64{},
	}
	buckets, err := s.loadMinuteBuckets(ctx, orgID, window)
	if err != nil {
		return out, err
	}
	out.DataAvailable = true
	for _, b := range buckets {
		out.TotalRequests += b.fields["requests"]
		out.ByStatusClass["2xx"] += b.fields["status_2xx"]
		out.ByStatusClass["3xx"] += b.fields["status_3xx"]
		out.ByStatusClass["4xx"] += b.fields["errors_4xx"]
		out.ByStatusClass["5xx"] += b.fields["errors_5xx"]
		out.TotalErrors += b.fields["errors_4xx"] + b.fields["errors_5xx"]
		for k, v := range b.fields {
			if strings.HasPrefix(k, "out_") && v > 0 {
				out.ByOutcome[strings.TrimPrefix(k, "out_")] += v
			}
		}
	}
	return out, nil
}

func estimatePercentileMS(counts []int64, p float64) float64 {
	var total int64
	for _, c := range counts {
		total += c
	}
	if total == 0 {
		return 0
	}
	target := int64(math.Ceil(float64(total) * p))
	var cum int64
	for i, c := range counts {
		cum += c
		if cum >= target {
			if i < len(latencyUpperBounds) {
				return latencyUpperBounds[i]
			}
			return 5000
		}
	}
	return 5000
}
