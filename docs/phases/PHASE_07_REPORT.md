# Phase 07 Report — Distributed Rate Limits and Quotas

- **Branch:** `phase/07-rate-limits`
- **PR:** Pending PR creation
- **Merge commit:** Pending merge
- **Date:** 2026-11-22

## Completed Tasks (01–11)

1. `2497380d51a8762cf304aa481fb189baf03acdc9` — `phase 07: define limiter policy schema`
2. `4010faeca6494869c0e072c78d5511539e70daee` — `phase 07: implement local token-bucket baseline`
3. `e6c6aa15deddd3a44451f3c3ef511bd192165e1f` — `phase 07: implement Redis distributed limiter`
4. `f1d794e7452d069bbbefdd98dab9bac97ef7f6a7` — `phase 07: implement fair key derivation`
5. `934049cba9318c64df90ab35f04c21ca84cb7bb1` — `phase 07: implement quota counters`
6. `2001e3e31c7538ff9da0dc48e41badad02201f67` — `phase 07: implement over-limit responses`
7. `6d8f7628712b004117044199ca0bfbdc8bacd6e4` — `phase 07: implement fail-mode policy`
8. `f28825d28a5c175ff6daf7c47ddcd044597252c2` — `phase 07: implement config change invalidation`
9. `aad90f556e62badd69c80c45d7cab3f97fa3c6f8` — `phase 07: implement multi-replica limit consistency`
10. `6b8a39d2c461838db98c6f56490d5c91928fd991` — `phase 07: export limiter telemetry`
11. *(HEAD commit of this PR)* — `phase 07: document and load-test limiter`

## Verification Evidence

- `make verify`: PASS (`go fmt`, `go vet`, `golangci-lint` 0 issues, ESLint, `tsc --noEmit`, `go test -race ./...`, Vitest, Go/Web builds, `docker compose config --quiet`).
- `make integration`: PASS including:
  - `TestRedisDistributedLimiterConcurrencyAndIsolation`: 40 concurrent goroutines on live Redis -> exactly 10 allowed, 30 denied; separate org isolated.
  - `TestTwoReplicaGlobalRateLimitConsistencyAndFailModes`: 40 concurrent requests split 20/20 across TWO synced gateway replicas -> exactly 12 allowed, 28 denied with `Retry-After` & `RateLimit` headers; Redis-outage replica serves `200 + X-RateLimit-Mode: fail-open-degraded` on `fail_open` routes and `503` on `critical`/`fail_closed` routes.
- Live load benchmark (`LOAD_TESTS=1 go test -v -run TestRateLimitThroughputBenchmark ./tests/load/...`):
  - Redis available: **16,579.7 RPS**, p50 **0.80 ms**, p95 **1.90 ms**, p99 **2.54 ms** (259 allowed vs 260 theoretical, 49,488 `429`).
  - Redis unavailable (`fail_open`): **13,709.9 RPS**, p50 **1.04 ms**, p95 **2.16 ms**, 0 errors.
  - Redis unavailable (`fail_closed`): **116,341.0 RPS**, p50 **0.11 ms**, p95 **0.38 ms**, 100% blocked with `503`.
  - Full report in `docs/benchmarks/RATELIMIT_BENCHMARK.md` and k6 script in `tests/load/ratelimit_load.js`.

## Confirmation

- No secrets committed, 11 atomic commits on `phase/07-rate-limits`, clean working tree.
