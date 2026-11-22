# Phase 07 Rate-Limit & Quota Load Benchmark

## Host Environment Metadata

- **Date:** 2026-11-22
- **OS / Arch:** Darwin `arm64` (macOS)
- **CPU:** Apple M4 (10 cores)
- **Memory:** 16 GiB RAM
- **Go Runtime:** Go 1.25.5
- **Backing Services:** Docker Compose Redis 7 (`127.0.0.1:6389`) and `mock-upstream` (`127.0.0.1:18089`)
- **Concurrency / Duration:** 16 concurrent HTTP keep-alive workers, 3.0s per profile
- **Policy Under Test:** `requests_per_window = 200`, `window_seconds = 10` (20 req/s refill), `burst = 200`, `key_by = ip`

## Measured Results (Reproducible Go Harness)

Command executed:
```bash
LOAD_TESTS=1 go test -v -run TestRateLimitThroughputBenchmark ./tests/load/...
```

| Profile | Total Reqs | Throughput (RPS) | HTTP 200 | HTTP 429 | HTTP 503 | Errors | p50 | p95 | p99 |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| `redis_available_distributed` | 49,747 | 16,579.7 | 259 | 49,488 | 0 | 0 | 0.80 ms | 1.90 ms | 2.54 ms |
| `redis_unavailable_fail_open` | 41,134 | 13,709.9 | 41,134 | 0 | 0 | 0 | 1.04 ms | 2.16 ms | 2.72 ms |
| `redis_unavailable_fail_closed` | 349,207 | 116,341.0 | 0 | 0 | 349,207 | 0 | 0.11 ms | 0.38 ms | 0.58 ms |

### Observations

1. **Distributed Accuracy:** Over 3.0 seconds with `burst=200` and refill `20 req/s`, theoretical maximum allowed requests is `200 + 60 = 260`; the live Redis Lua token bucket allowed **259** (`0.38%` within theoretical ceiling) and rejected **49,488** with `429 Too Many Requests` + `Retry-After` / `RateLimit` headers at **1.90 ms p95**.
2. **Fail-Open Behavior:** Standard routes configured with `fail_open` degraded cleanly to `mock-upstream` with `X-RateLimit-Mode: fail-open-degraded` at **13,709.9 RPS** and **2.16 ms p95**, zero errors.
3. **Fail-Closed Protection:** Critical routes (`security_class=critical`, `fail_closed`) never bypassed controls during Redis outage, returning `503 Service Unavailable` at **116,341.0 RPS** and **0.38 ms p95**.

## Reproducible k6 Script

A ready-to-run k6 script is provided at [ratelimit_load.js](../../tests/load/ratelimit_load.js):

```bash
# With native k6 installed:
k6 run -e GATEWAY_URL=http://127.0.0.1:18081 -e GATEWAY_HOST=bench.gateway.test -e VUS=20 -e DURATION=10s tests/load/ratelimit_load.js

# Or via Docker without local k6 installation:
docker run --rm --network host -v "$PWD/tests/load:/scripts:ro" grafana/k6 run \
  -e GATEWAY_URL=http://127.0.0.1:18081 -e GATEWAY_HOST=bench.gateway.test /scripts/ratelimit_load.js
```
