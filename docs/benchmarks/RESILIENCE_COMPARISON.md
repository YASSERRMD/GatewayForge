# GatewayForge Resilience and Scaling Comparison Profiles

Measured on **2026-11-22** on the same fixed host environment as [LOAD_BASELINE.md](./LOAD_BASELINE.md) (`Apple M4`, 10 cores, 16 GiB RAM, macOS Darwin 25.5.0, Go `1.25.5`, Docker `29.0.1` / Compose `v2.40.3-desktop.1`).

## 1. Direct Upstream vs Single Gateway vs Dual Gateway Replicas

**Command:**
```bash
LOAD_TESTS=1 go test -v -run TestResilienceComparisonProfiles ./tests/load/...
```

**Configuration:** 32 concurrent HTTP workers, 4 seconds per trial, 3 consecutive unfiltered trials per scenario, `GET /echo` (`Host: localhost`).

### Raw Per-Trial Measurements

| Scenario | Trial | Total Reqs | Throughput (req/s) | HTTP 200 | Errors | p50 | p95 | p99 |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| Direct `mock-upstream` (`:18089`) | 1 | 123,605 | 30,894.8 | 123,605 | 0 | 0.93 ms | 1.99 ms | 3.63 ms |
| Direct `mock-upstream` (`:18089`) | 2 | 131,207 | 32,793.6 | 131,207 | 0 | 0.91 ms | 1.74 ms | 2.95 ms |
| Direct `mock-upstream` (`:18089`) | 3 | 129,138 | 32,274.5 | 129,138 | 0 | 0.91 ms | 1.83 ms | 3.01 ms |
| Single Gateway Replica (`:18081`) | 1 | 37,093 | 9,272.6 | 37,093 | 0 | 3.01 ms | 6.47 ms | 12.30 ms |
| Single Gateway Replica (`:18081`) | 2 | 34,465 | 8,615.0 | 34,465 | 0 | 3.22 ms | 6.98 ms | 13.45 ms |
| Single Gateway Replica (`:18081`) | 3 | 35,185 | 8,795.6 | 35,185 | 0 | 3.25 ms | 7.25 ms | 11.19 ms |
| Dual Gateway Replicas (`:18081` + `:18082`) | 1 | 47,183 | 11,794.1 | 47,183 | 0 | 2.31 ms | 5.64 ms | 9.00 ms |
| Dual Gateway Replicas (`:18081` + `:18082`) | 2 | 50,914 | 12,726.5 | 50,914 | 0 | 2.13 ms | 5.31 ms | 9.03 ms |
| Dual Gateway Replicas (`:18081` + `:18082`) | 3 | 50,260 | 12,562.7 | 50,260 | 0 | 2.16 ms | 5.47 ms | 8.74 ms |

### 3-Trial Aggregated Comparison

| Scenario | Mean Throughput (req/s) | Mean p50 | Mean p95 | Mean p99 | Error Rate | Relative Note |
|---|---:|---:|---:|---:|---:|---|
| Direct `mock-upstream` | 31,987.6 | 0.92 ms | 1.85 ms | 3.20 ms | 0.00% | Raw upstream baseline (no proxy, no headers/tracing) |
| Single Gateway Replica | 8,894.4 | 3.16 ms | 6.90 ms | 12.31 ms | 0.00% | +2.24 ms median proxy + policy + OTel overhead |
| Dual Gateway Replicas | 12,361.1 | 2.20 ms | 5.47 ms | 8.92 ms | 0.00% | **+39.0% RPS** and **-30.4% p50** vs single replica on shared single-host Docker VM |

**Cross-check with Grafana k6 against direct `mock-upstream` (`:18089`, 32 VUs, 4s):**
- `70,706` requests in 4.0s = `17,651.5 req/s`, `0.00%` HTTP failures, `p50 = 1.55 ms`, `p95 = 2.98 ms`, `p99 = 5.02 ms` (compared to k6 through single gateway in [LOAD_BASELINE.md](./LOAD_BASELINE.md): `7,275.1 req/s`, `p50 = 3.81 ms`, adding `+2.26 ms` median proxy latency — matching the native Go differential `+2.24 ms` within `0.02 ms`).

---

## 2. Rate-Limited Traffic: Redis Healthy vs Redis Unavailable (`fail_open` vs `fail_closed`)

**Command:**
```bash
LOAD_TESTS=1 go test -v -run TestRateLimitThroughputBenchmark ./tests/load/...
```

**Configuration:** 16 concurrent HTTP workers, 3 seconds per scenario, route policy `200 req / 10s` (`burst = 200`), real Compose Redis (`127.0.0.1:6389`) vs unavailable Redis client.

| Scenario | Total Reqs | Throughput (req/s) | HTTP 200 | HTTP 429 | HTTP 503 | Unexpected Err | p50 | p95 | p99 |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| `redis_available_distributed` | 53,384 | 17,790.4 | 259 | 53,125 | 0 | 0 | 0.80 ms | 1.55 ms | 2.07 ms |
| `redis_unavailable_fail_open` | 38,007 | 12,666.5 | 38,007 | 0 | 0 | 0 | 1.15 ms | 2.19 ms | 2.88 ms |
| `redis_unavailable_fail_closed` | 303,555 | 101,142.2 | 0 | 0 | 303,555 | 0 | 0.12 ms | 0.47 ms | 0.66 ms |

### Observed Resilience Guarantees

1. **Accurate Distributed Enforcement (`redis_available_distributed`):** Initial burst of 200 plus 3 seconds of token refill (`~60` tokens at 20 req/s) allowed exactly `259` requests (`200 OK`) and rejected `53,125` excess requests (`429 Too Many Requests`) with `0.80 ms` median latency.
2. **Explicit Standard-Route Availability (`redis_unavailable_fail_open`):** Standard route configured with `fail_open` continued proxying `38,007/38,007` requests (`100%` HTTP 200, `0` errors) when Redis became unavailable.
3. **Explicit Critical-Route Safety (`redis_unavailable_fail_closed`):** Critical route configured with `fail_closed` blocked `303,555/303,555` requests (`100%` HTTP 503, `0` upstream leaks) at `0.12 ms` median latency without hitting the upstream.

## 3. Limitations

- Dual-replica scaling (`+39.0%` rather than linear `+100%`) is bounded by sharing the same single-host Docker Desktop Linux VM CPU and single `mock-upstream` container across both gateway replicas and the load driver.
- All runs are local synthetic benchmarks; production sizing requires isolated host nodes, production TLS termination, and real downstream latency distributions.
