# GatewayForge

GatewayForge is a self-hosted, multi-tenant API gateway and management studio built with a Go control plane, a Go reverse-proxy data plane, PostgreSQL, Redis, and a Next.js management dashboard.

## Architecture

- **Control Plane (`cmd/control-plane`, `internal/control`)**: Authenticated administrative API, configuration validation, draft/publication compiler, RBAC, and audit trail.
- **Gateway Data Plane (`cmd/gateway`, `internal/gateway`)**: High-concurrency HTTP reverse proxy reading immutable versioned configuration snapshots; never holds admin-write credentials.
- **Shared Contracts (`internal/shared`)**: Versioned snapshot schemas, environment configuration validation, and shared utilities.
- **Management Dashboard (`apps/web`)**: Next.js App Router TypeScript interface for operators and developers.
- **Infrastructure (`deploy/`, `migrations/`)**: PostgreSQL, Redis, deterministic mock upstream, and reproducible Docker Compose profiles.

## Repository Layout

```text
apps/web/              Next.js dashboard
cmd/control-plane/     Go control-plane entrypoint
cmd/gateway/           Go gateway data-plane entrypoint
internal/control/      Control-plane services, auth, persistence, compiler
internal/gateway/      Gateway proxy, routing, middleware, snapshot apply
internal/shared/       Versioned contracts and shared primitives
migrations/            Versioned SQL migrations
api/                   OpenAPI 3.1 contract
deploy/                Dockerfiles, Compose profiles, sample config
tests/integration/     Cross-service integration tests
tests/e2e/             Playwright and traffic E2E tests
tests/load/            k6 load test profiles
docs/adr/              Architecture decision records
docs/security/         Threat model and security evidence
docs/benchmarks/       Execution log, scorecard, load measurements
docs/phases/           Verified completion reports per phase
```

## Quick Commands

Run `make help` to inspect available development and verification targets.
