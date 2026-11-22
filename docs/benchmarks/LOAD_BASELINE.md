# GatewayForge Baseline Load Profile

Measured on **2026-11-22** against the live local Docker Compose stack (`docker compose up -d --wait` + `make seed`).

## 1. Environment & Hardware Context

| Parameter | Observed Value |
|---|---|
| Host Machine / Arch | Apple M4 (`arm64`), 10 CPU cores, 16 GiB RAM |
| OS / Kernel | macOS (Darwin 25.5.0, `RELEASE_ARM64_T8132`) |
| Go Version | `go1.25.5 darwin/arm64` |
| Docker / Compose | Docker `29.0.1` / Docker Compose `v2.40.3-desktop.1` (7.653 GiB Linux VM limit) |
| Load Generators | `grafana/k6:latest` (`sha256:3d8e5c7f1d53...`) and native Go HTTP driver (`tests/load/baseline_bench_test.go`) |
| Target Route | `GET /echo` (`Host: localhost`) -> `gateway-1` (`127.0.0.1:18081`) -> `mock-upstream` (`mock-upstream:8089`) |
| Request / Response Shape | Empty GET body; ~436-byte JSON echo response with `X-Request-Id` and W3C `Traceparent` headers |
| Concurrency & Duration | 32 concurrent virtual users / workers, 5 seconds per trial, 3 consecutive trials (unfiltered) |

## 2. Reproducible Commands

```bash
# 1. Grafana k6 baseline script (tests/load/baseline_load.js)
docker run --rm -i \
  -e TARGET_URL=http://host.docker.internal:18081 \
  -e TARGET_HOST=localhost \
  -e TARGET_PATH=/echo \
  -e VUS=32 \
  -e DURATION=5s \
  grafana/k6:latest run --summary-trend-stats="min,avg,med,p(90),p(95),p(99),max" - \
  < tests/load/baseline_load.js

# 2. Native Go baseline driver (tests/load/baseline_bench_test.go)
LOAD_TESTS=1 go test -v -run TestBaselineGatewayLoadProfile ./tests/load/...
```

## 3. Measured Results — Grafana k6 (3 Consecutive Trials)

| Trial | Total Requests | Throughput (req/s) | Failed (%) | Avg | p50 (med) | p90 | p95 | p99 | Max |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| Trial 1 | 35,966 | 7,190.6 | 0.00% (0) | 4.34 ms | 3.93 ms | 6.91 ms | 8.14 ms | 11.30 ms | 93.94 ms |
| Trial 2 | 36,321 | 7,261.1 | 0.00% (0) | 4.22 ms | 3.75 ms | 6.54 ms | 7.87 ms | 14.35 ms | 44.74 ms |
| Trial 3 | 36,896 | 7,373.6 | 0.00% (0) | 4.23 ms | 3.74 ms | 6.61 ms | 8.15 ms | 13.84 ms | 61.80 ms |
| **3-Trial Mean** | **36,394** | **7,275.1** | **0.00%** | **4.26 ms** | **3.81 ms** | **6.69 ms** | **8.05 ms** | **13.16 ms** | — |

All k6 thresholds passed on every trial (`http_req_failed rate<0.01`, `p(95)<100ms`, `p(99)<250ms`), with `100.00%` check pass rate across all `109,183` HTTP requests (`status is 200` + `has X-Request-Id`).

## 4. Measured Results — Native Go HTTP Driver (3 Consecutive Trials)

| Trial | Total Requests | Throughput (req/s) | HTTP 200 | Errors | p50 | p95 | p99 |
|---|---:|---:|---:|---:|---:|---:|---:|
| Trial 1 | 43,686 | 8,736.2 | 43,686 | 0 | 3.29 ms | 7.04 ms | 10.25 ms |
| Trial 2 | 42,255 | 8,450.0 | 42,255 | 0 | 3.29 ms | 7.37 ms | 14.55 ms |
| Trial 3 | 44,583 | 8,900.1 | 44,583 | 0 | 3.13 ms | 7.07 ms | 13.70 ms |
| **3-Trial Mean** | **43,508** | **8,695.4** | **43,508** | **0** | **3.24 ms** | **7.16 ms** | **12.83 ms** |

## 5. Container Resource Footprint (Captured Immediately Post-Load)

| Container | Memory Usage | VM Memory % |
|---|---:|---:|
| `gatewayforge-gateway-1` | 22.80 MiB / 7.653 GiB | 0.29% |
| `gatewayforge-gateway-2-1` | 13.79 MiB / 7.653 GiB | 0.18% |
| `gatewayforge-mock-upstream-1` | 15.60 MiB / 7.653 GiB | 0.20% |
| `gatewayforge-redis-1` | 11.66 MiB / 7.653 GiB | 0.15% |
| `gatewayforge-control-plane-1` | 16.29 MiB / 7.653 GiB | 0.21% |

## 6. Methodological Constraints & Honest Caveats

- Load generator, gateway containers, mock upstream, and Docker Linux VM share the same laptop CPU and loopback/virtual network bridge; absolute RPS figures are **local single-host reference numbers**, not production capacity claims.
- The Dockerized k6 client traverses `host.docker.internal` + Docker Desktop's virtual NAT layer twice, explaining why native host Go requests (`8,695 req/s`) measure ~19% higher throughput than containerized k6 (`7,275 req/s`).
- Baseline `/echo` exercises route matching, header sanitization, correlation ID generation, OTel tracing span creation, timeout policy evaluation, CORS check, SSRF-guarded transport pooling, and reverse proxying without Redis rate limiting (see [RESILIENCE_COMPARISON.md](./RESILIENCE_COMPARISON.md) for direct vs single vs dual replica and Redis-healthy vs Redis-failed comparisons).
