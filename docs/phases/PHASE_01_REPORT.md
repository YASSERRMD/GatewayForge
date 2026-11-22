# Phase 01 Report — Repository Foundation and Runnable Services

- **Branch:** `phase/01-foundation`
- **PR:** Pending PR creation (updated in cumulative log after merge)
- **Merge commit:** Pending merge
- **Date:** 2026-11-22

## Completed Tasks (01–11)

1. `a09d1b5d526a30905cf3a64cb58ba87b10fedfb1` — `phase 01: create monorepo structure`
2. `ea279a04f911621320d33c440ccb647130526161` — `phase 01: bootstrap Go module`
3. `103625796fff3c04c92fb1d04b9082dbe98e7d14` — `phase 01: serve control-plane health`
4. `5874097e92d6ee2754c8a98b73f19d9220d7244d` — `phase 01: serve gateway health`
5. `46dc3d38d35a6d5713fa2c325df6ca951c2e206b` — `phase 01: create Next.js shell`
6. `3f24ad881715f2eec38bc7d1a527481f4b0a5462` — `phase 01: validate environment configuration`
7. `6f8b6cf984f1dbb7aa76ed1986f11c491527f3b9` — `phase 01: start PostgreSQL in Compose`
8. `e5a2d2ed6199bc6fc54df286fc06ba35aaae844d` — `phase 01: start Redis in Compose`
9. `326891e2c012e3e5d7473fdd95cf71210b42ccf7` — `phase 01: add deterministic mock upstream`
10. `10200b8588b4b34e553debd9eec93c5403446800` — `phase 01: standardize tooling and Makefile`
11. `6a0dcac7261bf71fe2f2ea5a01dfa0c1b6ebc125` — `phase 01: add baseline GitHub CI`
12. *(CI fix commit)* — `phase 01: pin golangci-lint v2.12.2 in CI` (fixed `golangci-lint-action` rejecting `version: v2` without minor/patch version)

## Verification Evidence

- `make verify`: PASSED (layout check 17/17 dirs, `go fmt`, `go vet`, `golangci-lint` 0 issues, ESLint 0 errors, TypeScript strict `tsc --noEmit`, `go test -race ./...`, Vitest 1/1 passed, Go builds, Next.js production build, `docker compose config --quiet`).
- Negative quality gate verification: deliberately inserted a `fmt.Sprintf("%d", "not-an-int")` fixture and confirmed `make lint-go` rejected it before removing the fixture.
- `make compose-up`: All 6 containers (`postgres`, `redis`, `mock-upstream`, `control-plane`, `gateway`, `web`) built and reached `Healthy`.
- `make integration`: PASSED (`TestPostgresConnectivitySmoke`, `TestRedisConnectivitySmoke`, `TestMockUpstreamComposeSmoke`).
- Live HTTP smoke checks:
  - `GET http://127.0.0.1:18080/health/live` -> `200 {"status":"ok","service":"control-plane",...}`
  - `GET http://127.0.0.1:18080/health/ready` -> `200 {"status":"ready","service":"control-plane",...}`
  - `GET http://127.0.0.1:18081/health/live` -> `200 {"status":"ok","service":"gateway",...}`
  - `GET http://127.0.0.1:18081/health/ready` -> `200 {"status":"ready","service":"gateway",...}`
  - `HEAD http://127.0.0.1:3000/` -> `HTTP/1.1 200 OK`
- `bash scripts/reset-db.sh`: Verified clean schema drop/recreate on local PostgreSQL.

## Architecture, Environment and Security Notes

- Enforced compile-time package isolation in `internal/shared/buildinfo/buildinfo_test.go` (`internal/control` and `internal/gateway` cannot import each other; `internal/shared` imports neither).
- Gateway listener defaults to deny-all JSON 404 (`route_not_found`) — never an open proxy.
- Environment loader validates all required fields and never echoes secret values in errors or redacted log summaries.
- Default Compose host ports are bound strictly to `127.0.0.1` (`5439`, `6389`, `18080`, `18081`, `18089`, `3000`) to avoid colliding with existing host services while keeping standard container ports internally.

## Known Risks and Not-Yet-Applicable Checks

- `make e2e`, `make security-test`, and `make load` are not yet implemented in Phase 01 (scheduled for later phases).
- Control-plane and gateway readiness probes are wired for dependency injection and will attach live PostgreSQL/Redis/snapshot probes in Phases 02–05.

## Confirmation

- No secrets committed (`.env.example` contains dummy local development values only).
- 11 atomic commits on `phase/01-foundation`, no squashing or rebasing, clean working tree.
