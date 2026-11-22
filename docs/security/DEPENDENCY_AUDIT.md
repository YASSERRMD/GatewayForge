# GatewayForge Dependency and Container Security Audit

- **Audit Date:** 2026-11-22
- **Pinned Tooling:** Go `1.25.5`, Node.js `v24.11.1`, npm `11.6.2`, Docker `29.0.1`

## 1. Frontend (`apps/web`) `npm audit` Results & Honest Triage

### Commands Executed
```bash
cd apps/web && npm audit --omit=dev
cd apps/web && npm audit
```

### Observed Findings (Measured 2026-11-22)
| Package | Scope | Advisory / Severity | Reachability & Triage | Planned Remediation |
|---|---|---|---|---|
| `postcss <=8.5.22` (bundled under `next@15.5.29`) | Build-time transitive (`next`) | High (`GHSA-qx2v-qp2m-jg93`, `GHSA-6g55-p6wh-862q`, `GHSA-fxqj-rqcc-2cmp`, `GHSA-r28c-9q8g-f849`) | **Not reachable from untrusted input at runtime.** PostCSS only compiles trusted first-party CSS during `next build`; GatewayForge never accepts or compiles user-supplied CSS or source maps. | Upgrade to Next.js 16.x once Next 16 ecosystem migration is scheduled (currently a breaking major bump from pinned Next.js 15). |
| `tinypool <=2.1.1` & `@vitest/mocker <=4.1.11` (under `vitest@3.2.7`) | **Dev-only** (`devDependencies`) | Critical / Moderate (`GHSA-5gmw-xhrv-c9v3`, `GHSA-85c8-ppgw-ccpr`, `GHSA-82fw-gwwq-j7x9`, `GHSA-qxg6-89mg-rqv8`) | **Excluded from production runtime.** `deploy/Dockerfile.web` builds Next.js standalone output and copies ONLY `.next/standalone` and `.next/static` into the final `runner` stage; `vitest`/`tinypool` are never installed or executed in production containers. | Upgrade to `vitest@5.x` during the next dev-tooling major upgrade window. |

## 2. Go Module Inventory (`go.mod`)
Command: `go list -m all`
- Core direct dependencies pinned via `go.sum`:
  - `github.com/go-chi/chi/v5 v5.2.1`
  - `github.com/jackc/pgx/v5 v5.7.5`
  - `github.com/redis/go-redis/v9 v9.8.0`
  - `go.opentelemetry.io/otel v1.34.0`
  - `golang.org/x/crypto v0.37.0`
  - `gopkg.in/yaml.v3 v3.0.1`
- All Go binaries are compiled statically (`CGO_ENABLED=0`) with `-trimpath` to eliminate local filesystem path leakage and native C runtime attack surface.
- Note: `govulncheck` is not pre-installed in the local environment; running `go run golang.org/x/vuln/cmd/govulncheck@latest ./...` is recommended in scheduled CI security scans.

## 3. Container & Dockerfile Hardening Audit
Verified automatically by `scripts/check-containers.sh` across all 4 production Dockerfiles (`deploy/Dockerfile.control-plane`, `deploy/Dockerfile.gateway`, `deploy/Dockerfile.mock-upstream`, `deploy/Dockerfile.web`):
- **Multi-stage builds**: Every Dockerfile compiles in a builder stage and copies only minimal runtime artifacts to an Alpine runtime stage.
- **Pinned base images**: `golang:1.25.5-alpine`, `alpine:3.21`, `node:24-alpine` (zero `:latest` tags).
- **Non-root execution**: Every container creates and switches to dedicated UID `10001` (`USER appuser` / `USER nextjs`).
- **Network binding**: `docker-compose.yml` binds all host-exposed ports strictly to `127.0.0.1` so local development instances are never accidentally exposed on `0.0.0.0`.
- **No baked secrets**: Runtime secrets (`SESSION_SECRET`, `GATEWAY_SYNC_TOKEN`, `POSTGRES_PASSWORD`, `REDIS_PASSWORD`) are injected solely via environment variables and never baked into Docker layers.
