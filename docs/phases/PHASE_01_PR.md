## Scope

Establishes the GatewayForge monorepo foundation, Go module boundaries, independent control-plane and gateway HTTP servers with health/readiness endpoints, Next.js TypeScript strict dashboard shell, typed secret-safe environment validation, Docker Compose stack (PostgreSQL 16, Redis 7, deterministic mock upstream, control-plane, gateway, web), standardized linting/Makefile targets, and baseline GitHub Actions CI.

## Task Commits (11 Atomic Commits)

1. `phase 01: create monorepo structure` (`a09d1b5`)
2. `phase 01: bootstrap Go module` (`ea279a0`)
3. `phase 01: serve control-plane health` (`1036257`)
4. `phase 01: serve gateway health` (`5874097`)
5. `phase 01: create Next.js shell` (`46dc3d3`)
6. `phase 01: validate environment configuration` (`3f24ad8`)
7. `phase 01: start PostgreSQL in Compose` (`6f8b6cf`)
8. `phase 01: start Redis in Compose` (`e5a2d2e`)
9. `phase 01: add deterministic mock upstream` (`326891e`)
10. `phase 01: standardize tooling and Makefile` (`10200b8`)
11. `phase 01: add baseline GitHub CI` (PR tip)

## Verification

- `make verify`: PASS (`check-layout`, `go fmt`, `go vet`, `golangci-lint` 0 issues, ESLint, `tsc --noEmit`, `go test -race ./...`, Vitest 1/1, Go + Next.js builds, `docker compose config --quiet`)
- Negative lint gate test: PASS (deliberate `fmt.Sprintf` type mismatch rejected by `make lint-go`)
- `make compose-up` + `make integration`: PASS (all 6 services healthy; Postgres, Redis, and mock-upstream integration tests green)
- Live HTTP smoke: `/health/live` and `/health/ready` return 200 JSON on both control-plane (`18080`) and gateway (`18081`); Next.js shell returns `200 OK` on port `3000`.
- Not yet applicable in Phase 01: `make e2e`, `make security-test`, `make load`.

## Security & Architecture Notes

- Package isolation between `internal/control`, `internal/gateway`, and `internal/shared` is automatically enforced in `TestPackageBoundaries`.
- Gateway denies unmatched routes with structured 404 JSON (`route_not_found`).
- All Compose port bindings restrict host exposure to `127.0.0.1`.
- CI runs with `contents: read` least-privilege permissions.
