# Phase 07: Distributed Rate Limits and Quotas

## Summary
Implements typed rate-limit and UTC quota policy schemas with control-plane validation, deterministic injectable-clock local token-bucket and quota baselines, atomic Redis Lua distributed rate limiting and quotas, collision-free secret-free hashed key derivation, RFC-style `RateLimit`/`RateLimit-Policy`/`Retry-After` 429 headers, explicit `fail_open` vs `fail_closed` Redis outage handling (never silently bypassing `critical` routes), config-digest key namespace invalidation preventing stale privilege escalation, two-replica global limit consistency verification, bounded-cardinality limiter telemetry (`/health/ratelimit`), and reproducible load testing (`tests/load/ratelimit_load.js` + `docs/benchmarks/RATELIMIT_BENCHMARK.md`).

## Tasks Implemented (11 Atomic Commits)
1. `phase 07: define limiter policy schema`
2. `phase 07: implement local token-bucket baseline`
3. `phase 07: implement Redis distributed limiter`
4. `phase 07: implement fair key derivation`
5. `phase 07: implement quota counters`
6. `phase 07: implement over-limit responses`
7. `phase 07: implement fail-mode policy`
8. `phase 07: implement config change invalidation`
9. `phase 07: implement multi-replica limit consistency`
10. `phase 07: export limiter telemetry`
11. `phase 07: document and load-test limiter`

## Verification
- `make verify` — PASS
- `make integration` — PASS (including Redis atomic concurrency & two-replica global limit consistency)
- `LOAD_TESTS=1 go test -v -run TestRateLimitThroughputBenchmark ./tests/load/...` — PASS (see `docs/benchmarks/RATELIMIT_BENCHMARK.md`)
