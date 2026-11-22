package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/YASSERRMD/GatewayForge/internal/shared/policy"
)

// tokenBucketLua atomically executes a continuous token-bucket check using Redis server TIME
// so multiple gateway replicas share one clock without inter-node skew.
//
// KEYS[1] = bucket key
// ARGV[1] = burst capacity (float)
// ARGV[2] = refill_rate_per_ms (float = requests_per_window / (window_seconds * 1000))
// ARGV[3] = ttl_ms (integer)
//
// Returns: {allowed (0|1), remaining_int, retry_after_ms, reset_after_ms}
var tokenBucketLua = redis.NewScript(`
local key = KEYS[1]
local capacity = tonumber(ARGV[1])
local rate_per_ms = tonumber(ARGV[2])
local ttl_ms = tonumber(ARGV[3])

local t = redis.call("TIME")
local now_ms = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)

local data = redis.call("HMGET", key, "tokens", "ts_ms")
local tokens = tonumber(data[1])
local last_ms = tonumber(data[2])

if tokens == nil or last_ms == nil then
  tokens = capacity
  last_ms = now_ms
else
  local elapsed = now_ms - last_ms
  if elapsed > 0 then
    tokens = math.min(capacity, tokens + elapsed * rate_per_ms)
    last_ms = now_ms
  end
end

local allowed = 0
local retry_after_ms = 0
if tokens >= 1.0 then
  tokens = tokens - 1.0
  allowed = 1
else
  retry_after_ms = math.ceil((1.0 - tokens) / rate_per_ms)
end

local remaining = math.floor(tokens)
if remaining < 0 then remaining = 0 end
local reset_after_ms = math.ceil((capacity - tokens) / rate_per_ms)
if reset_after_ms < 0 then reset_after_ms = 0 end

redis.call("HSET", key, "tokens", string.format("%.6f", tokens), "ts_ms", tostring(last_ms))
redis.call("PEXPIRE", key, ttl_ms)

return {allowed, remaining, retry_after_ms, reset_after_ms}
`)

// RedisLimiter executes atomic distributed token-bucket rate limiting backed by Redis.
type RedisLimiter struct {
	client     redis.Cmdable
	cmdTimeout time.Duration
}

// NewRedisLimiter constructs a distributed RedisLimiter.
func NewRedisLimiter(client redis.Cmdable, cmdTimeout time.Duration) *RedisLimiter {
	if cmdTimeout <= 0 {
		cmdTimeout = 250 * time.Millisecond
	}
	return &RedisLimiter{
		client:     client,
		cmdTimeout: cmdTimeout,
	}
}

// Allow atomically evaluates pol for bucket key on Redis.
func (r *RedisLimiter) Allow(ctx context.Context, key string, pol policy.RateLimitPolicy) (Decision, error) {
	if r == nil || r.client == nil {
		return Decision{}, errors.New("redis limiter client is nil")
	}
	pol.Normalize()
	if err := pol.Validate(); err != nil {
		return Decision{}, err
	}

	window := time.Duration(pol.WindowSeconds) * time.Second
	ratePerMS := float64(pol.RequestsPerWindow) / float64(pol.WindowSeconds*1000)
	ttlMS := pol.WindowSeconds * 2000
	if ttlMS < 2000 {
		ttlMS = 2000
	}

	cmdCtx, cancel := context.WithTimeout(ctx, r.cmdTimeout)
	defer cancel()

	raw, err := tokenBucketLua.Run(cmdCtx, r.client, []string{key},
		strconv.FormatInt(pol.Burst, 10),
		strconv.FormatFloat(ratePerMS, 'f', 12, 64),
		strconv.FormatInt(ttlMS, 10),
	).Int64Slice()
	if err != nil {
		return Decision{}, fmt.Errorf("redis rate limit script: %w", err)
	}
	if len(raw) != 4 {
		return Decision{}, fmt.Errorf("unexpected redis rate limit result length %d", len(raw))
	}

	return Decision{
		Allowed:    raw[0] == 1,
		Limit:      pol.Burst,
		Remaining:  raw[1],
		RetryAfter: time.Duration(raw[2]) * time.Millisecond,
		ResetAfter: time.Duration(raw[3]) * time.Millisecond,
		Window:     window,
		Backend:    "redis",
	}, nil
}
