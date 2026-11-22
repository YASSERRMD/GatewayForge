# GatewayForge

**GatewayForge** is a production-oriented, self-hosted, multi-tenant API gateway and management studio built with a Go control plane, a high-throughput Go reverse-proxy data plane, PostgreSQL 16, Redis 7, and a Next.js 15 TypeScript dashboard.

## Highlights
- **Strict Control-Plane / Data-Plane Split**: Control plane manages RBAC, draft compilation, immutable checksummed snapshots, OpenAPI 3.0/3.1 import/export, and redacted audit trails; gateway replicas consume read-only snapshots with zero database write access.
- **Zero-Restart Convergence & Rollback**: Redis Pub/Sub hints + periodic reconciliation converge multi-replica gateways in milliseconds while preserving verified Last-Known-Good (LKG) snapshots across outages and restarts.
- **Defense-in-Depth Security**: Argon2id sessions + HttpOnly cookies + CSRF tokens, constant-time HMAC-SHA256 API keys (shown once), asymmetric RS256/ES256 JWT + bounded SSRF-safe JWKS, save-time & connect-time SSRF/DNS-rebinding guards, atomic Redis Lua distributed rate limits with `critical`/`fail_closed` invariants, and automatic secret log/audit redaction.
- **Operator Studio (`apps/web`)**: Live traffic overview, service/upstream/route/consumer/policy editors, safe gateway-anchored Request Console, semantic structural diffing, immutable release history, fleet convergence visibility, and Audit Explorer.

## Quickstart
See [docs/QUICKSTART.md](docs/QUICKSTART.md) for the full clean-room walkthrough.

```bash
cp .env.example .env
make dev
```
- **Management Studio:** `http://127.0.0.1:3000`
- **Control Plane API:** `http://127.0.0.1:18080`
- **Gateway Data Plane:** `http://127.0.0.1:18081` (replica 1) and `http://127.0.0.1:18082` (replica 2)

## Key Commands
Run `make help` for the complete list:
- `make dev` — Build & start Compose stack, run SQL migrations, and seed demo fixtures
- `make verify` — Run layout check, Go vet/lint/race tests/build, Web ESLint/tsc/Vitest/Next build, and Compose validation
- `make security-test` — Run Dockerfile hardening checks, secret scan, and adversarial security/chaos test suites
- `make integration` — Run PostgreSQL & Redis integration test suites (`INTEGRATION_TESTS=1`)
- `make e2e` — Run live Compose-backed Playwright end-to-end tests
- `make load` — Run reproducible gateway throughput & rate-limit benchmarks

## Documentation
- **Quickstart & Clean-Room Install:** [docs/QUICKSTART.md](docs/QUICKSTART.md)
- **Architecture & Configuration Reference:** [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)
- **Control Plane OpenAPI Contract:** [api/openapi.yaml](api/openapi.yaml)
- **Administrator Operations Runbook:** [docs/ops/ADMIN_RUNBOOK.md](docs/ops/ADMIN_RUNBOOK.md)
- **Observability & Tracing:** [docs/ops/OBSERVABILITY.md](docs/ops/OBSERVABILITY.md)
- **Threat Model & Security Gap Report:** [docs/security/THREAT_MODEL.md](docs/security/THREAT_MODEL.md) · [docs/security/SECURITY_REPORT.md](docs/security/SECURITY_REPORT.md)
- **Benchmarks & Scorecard:** [docs/benchmarks/LOAD_BASELINE.md](docs/benchmarks/LOAD_BASELINE.md) · [docs/benchmarks/SCORECARD.md](docs/benchmarks/SCORECARD.md)
