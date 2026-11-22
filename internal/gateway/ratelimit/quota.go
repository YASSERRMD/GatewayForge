package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/YASSERRMD/GatewayForge/internal/shared/policy"
)

// QuotaCounter tracks fixed-period UTC usage against a configured quota limit.
type QuotaCounter interface {
	Consume(ctx context.Context, keyInput KeyInput, q policy.QuotaPolicy) (Decision, error)
}

// UTCPeriodWindow computes the deterministic UTC bucket label, full window duration,
// and remaining duration until rollover for period at timestamp t.
func UTCPeriodWindow(t time.Time, period string) (bucket string, window time.Duration, resetAfter time.Duration, err error) {
	u := t.UTC()
	switch period {
	case policy.PeriodHourly:
		start := time.Date(u.Year(), u.Month(), u.Day(), u.Hour(), 0, 0, 0, time.UTC)
		end := start.Add(time.Hour)
		return u.Format("2006010215"), time.Hour, end.Sub(u), nil
	case policy.PeriodDaily:
		start := time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
		end := start.AddDate(0, 0, 1)
		return u.Format("20060102"), 24 * time.Hour, end.Sub(u), nil
	case policy.PeriodMonthly:
		start := time.Date(u.Year(), u.Month(), 1, 0, 0, 0, 0, time.UTC)
		end := start.AddDate(0, 1, 0)
		return u.Format("200601"), end.Sub(start), end.Sub(u), nil
	default:
		return "", 0, 0, fmt.Errorf("unsupported quota period %q", period)
	}
}

type localQuotaEntry struct {
	used      int64
	expiresAt time.Time
}

// LocalQuotaCounter is a concurrency-safe, UTC-aware local quota counter with bounded retention.
type LocalQuotaCounter struct {
	now        func() time.Time
	maxEntries int

	mu      sync.Mutex
	entries map[string]*localQuotaEntry
}

// NewLocalQuotaCounter creates a LocalQuotaCounter with an optional injectable clock.
func NewLocalQuotaCounter(now func() time.Time, maxEntries int) *LocalQuotaCounter {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if maxEntries <= 0 {
		maxEntries = 10000
	}
	return &LocalQuotaCounter{
		now:        now,
		maxEntries: maxEntries,
		entries:    make(map[string]*localQuotaEntry),
	}
}

// Consume atomically checks and increments the UTC period counter for keyInput.
func (c *LocalQuotaCounter) Consume(_ context.Context, keyInput KeyInput, q policy.QuotaPolicy) (Decision, error) {
	q.Normalize()
	if err := q.Validate(); err != nil {
		return Decision{}, err
	}
	now := c.now().UTC()
	bucket, window, resetAfter, err := UTCPeriodWindow(now, q.Period)
	if err != nil {
		return Decision{}, err
	}
	key := DeriveQuotaKey(keyInput, q, bucket)

	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.entries) >= c.maxEntries {
		for k, e := range c.entries {
			if now.After(e.expiresAt) {
				delete(c.entries, k)
			}
		}
	}

	entry, ok := c.entries[key]
	if !ok || now.After(entry.expiresAt) {
		entry = &localQuotaEntry{
			used:      0,
			expiresAt: now.Add(resetAfter + 5*time.Minute),
		}
		c.entries[key] = entry
	}

	if entry.used < q.Limit {
		entry.used++
		return Decision{
			Allowed:    true,
			Limit:      q.Limit,
			Remaining:  q.Limit - entry.used,
			ResetAfter: resetAfter,
			RetryAfter: 0,
			Window:     window,
			Backend:    "local",
		}, nil
	}

	return Decision{
		Allowed:    false,
		Limit:      q.Limit,
		Remaining:  0,
		ResetAfter: resetAfter,
		RetryAfter: resetAfter,
		Window:     window,
		Backend:    "local",
	}, nil
}

// quotaLua atomically checks and increments a fixed-period quota counter without exceeding limit.
// KEYS[1] = quota key
// ARGV[1] = limit
// ARGV[2] = ttl_ms
// Returns: {allowed (0|1), remaining}
var quotaLua = redis.NewScript(`
local key = KEYS[1]
local limit = tonumber(ARGV[1])
local ttl_ms = tonumber(ARGV[2])

local cur = tonumber(redis.call("GET", key) or "0")
if cur >= limit then
  return {0, 0}
end
local next_val = redis.call("INCR", key)
if next_val == 1 then
  redis.call("PEXPIRE", key, ttl_ms)
end
local rem = limit - next_val
if rem < 0 then rem = 0 end
return {1, rem}
`)

// RedisQuotaCounter tracks distributed UTC quotas atomically in Redis with bounded TTL retention.
type RedisQuotaCounter struct {
	client     redis.Cmdable
	cmdTimeout time.Duration
	now        func() time.Time
}

// NewRedisQuotaCounter creates a Redis-backed UTC QuotaCounter.
func NewRedisQuotaCounter(client redis.Cmdable, cmdTimeout time.Duration) *RedisQuotaCounter {
	if cmdTimeout <= 0 {
		cmdTimeout = 250 * time.Millisecond
	}
	return &RedisQuotaCounter{
		client:     client,
		cmdTimeout: cmdTimeout,
		now:        func() time.Time { return time.Now().UTC() },
	}
}

// Consume checks and increments the UTC period counter in Redis.
func (r *RedisQuotaCounter) Consume(ctx context.Context, keyInput KeyInput, q policy.QuotaPolicy) (Decision, error) {
	if r == nil || r.client == nil {
		return Decision{}, errors.New("redis quota client is nil")
	}
	q.Normalize()
	if err := q.Validate(); err != nil {
		return Decision{}, err
	}
	now := r.now().UTC()
	bucket, window, resetAfter, err := UTCPeriodWindow(now, q.Period)
	if err != nil {
		return Decision{}, err
	}
	key := DeriveQuotaKey(keyInput, q, bucket)
	ttlMS := (resetAfter + 5*time.Minute).Milliseconds()
	if ttlMS < 60_000 {
		ttlMS = 60_000
	}

	cmdCtx, cancel := context.WithTimeout(ctx, r.cmdTimeout)
	defer cancel()

	res, err := quotaLua.Run(cmdCtx, r.client, []string{key},
		strconv.FormatInt(q.Limit, 10),
		strconv.FormatInt(ttlMS, 10),
	).Int64Slice()
	if err != nil {
		return Decision{}, fmt.Errorf("redis quota script: %w", err)
	}
	if len(res) != 2 {
		return Decision{}, fmt.Errorf("unexpected redis quota result length %d", len(res))
	}

	allowed := res[0] == 1
	retryAfter := time.Duration(0)
	if !allowed {
		retryAfter = resetAfter
	}
	return Decision{
		Allowed:    allowed,
		Limit:      q.Limit,
		Remaining:  res[1],
		ResetAfter: resetAfter,
		RetryAfter: retryAfter,
		Window:     window,
		Backend:    "redis",
	}, nil
}
