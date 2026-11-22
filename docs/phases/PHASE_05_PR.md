## Scope

Completes the full dynamic configuration pipeline across control plane, PostgreSQL, Redis, and multi-replica gateway instances: tenant-scoped Services, Upstreams (with shared SSRF validation), and Routes CRUD + precedence preview APIs; deterministic draft compiler and exhaustive pre-publish validator; concurrency-safe immutable publication and rollback transactions; rotation-ready authenticated gateway snapshot pull endpoint with ETag/304; Redis post-commit invalidation hints with coalescing; periodic reconciliation poller; atomic disk-backed last-known-good fallback; and automated two-gateway zero-restart convergence verification.

## Task Commits (11 Atomic Commits)

1. `phase 05: implement service API` (`220ef7e`)
2. `phase 05: implement upstream API` (`4f46c24`)
3. `phase 05: implement route API` (`1346cfd`)
4. `phase 05: implement draft configuration compiler` (`ce8ac50`)
5. `phase 05: implement configuration validation endpoint` (`b92f823`)
6. `phase 05: implement publication transaction` (`13d1b9a`)
7. `phase 05: implement authenticated snapshot retrieval` (`3ee39c3`)
8. `phase 05: implement Redis config-change events` (`39d465b`)
9. `phase 05: implement gateway snapshot polling` (`e657757`)
10. `phase 05: implement last-known-good fallback` (`0f085ac`)
11. `phase 05: verify two-gateway convergence` (PR tip)

## Verification

- `make verify`: PASS (`go fmt`, `go vet`, `golangci-lint` 0 issues, `go test -race ./...`, web lint/typecheck/test/build, `docker compose config`)
- `make integration`: PASS (all Phase 01–05 integration suites including `TestTwoGatewayReplicaZeroRestartConvergence`, concurrent optimistic publish race, token rotation, Redis pub/sub coalescing, and LKG fallback)
- Not yet applicable in Phase 05: `make e2e`, `make security-test`, `make load`.
