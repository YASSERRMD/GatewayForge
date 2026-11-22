# ADR-0001: Monorepo Layout and Control/Data Plane Separation

- **Status:** Accepted
- **Date:** 2026-11-22

## Context

GatewayForge comprises a Go control plane, a Go reverse-proxy gateway data plane, a Next.js management UI, SQL migrations, OpenAPI contracts, and multi-layer test suites. Keeping these components coordinated across versioned configuration contracts requires clear module boundaries without introducing circular dependencies or blending administrative privileges into traffic-handling processes.

## Decision

1. Use a single monorepo with explicit top-level directories separating entrypoints (`cmd/`), private Go packages (`internal/control`, `internal/gateway`, `internal/shared`), web UI (`apps/web`), migrations (`migrations/`), API specs (`api/`), deployment definitions (`deploy/`), tests (`tests/`), and documentation (`docs/`).
2. Enforce strict Go package direction:
   - `cmd/control-plane` may import `internal/control` and `internal/shared`, never `internal/gateway`.
   - `cmd/gateway` may import `internal/gateway` and `internal/shared`, never `internal/control`.
   - `internal/shared` holds only versioned contracts, config primitives, and infrastructure helpers; it never imports `internal/control` or `internal/gateway`.
3. Separate runtime trust boundaries:
   - The control plane owns write access to PostgreSQL and emits invalidation hints to Redis.
   - The gateway data plane consumes read-only published configuration snapshots via an authenticated pull channel and uses Redis for distributed rate limiting and invalidation hints.
   - The dashboard interacts exclusively with the control plane API and is never in the gateway request path.

## Consequences

- Control-plane and gateway binaries can be built, tested, scaled, and deployed independently.
- Accidental coupling between admin handlers and proxy handlers is prevented at compile time and verified in CI.
