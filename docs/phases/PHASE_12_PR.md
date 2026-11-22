# Phase 12 — Developer Request Console and OpenAPI Import

## Summary
Delivers the safe, gateway-anchored Developer Request Console (`/console`) and a complete OpenAPI 3.0/3.1 upload, parsing, preview, collision resolution, transactional import, and documented export workflow.

## Key Changes
- **Developer Request Console (`/console`)**:
  - Route prefill, method/host/path/headers/body editor with 64 KiB cap, `Ctrl/Cmd+Enter` execution, and RBAC (`console:use`) enforcement.
  - Constrained backend transport `POST /api/v1/orgs/{orgId}/console/execute` anchored strictly to `GATEWAY_PROXY_URL` — rejects arbitrary URLs/traversal/port smuggling, strips spoofed forwarded headers, disables redirects, enforces 256 KiB response preview cap, and detects binary payloads.
  - Safe Response Panel with XSS-safe plain-text escaping, binary rendering suppression, truncation notices, headers inspector, and `.txt` safe download.
  - Opt-in bounded (20-entry) sanitized history stripping `Authorization`, `X-Api-Key`, `Cookie`, and secret query parameters before `localStorage` persistence, plus credential-free local/mock environment presets.
- **OpenAPI 3.0/3.1 Import & Export (`/routes` + `/api/v1/orgs/{orgId}/openapi/*`)**:
  - Safe JSON/YAML parser (`internal/control/openapi`) enforcing 2 MiB size cap, depth limit, and strict external `$ref` rejection.
  - Deterministic preview mapper with unsupported-construct warnings (`TRACE`, `callbacks`, multiple servers, path templates).
  - Explicit conflict resolution modal (`skip` default — never silent overwrite — with `rename` and `overwrite` options).
  - Single-transaction PostgreSQL draft import rolling back completely on partial failure and recording `openapi.import` audit events.
  - Deterministic OpenAPI 3.0.3 JSON export (`GET /openapi/export`) with explicit `x-gatewayforge-export-limitations`.
- **Golden E2E (`AT-17`)**: `apps/web/e2e/openapi-import-console.spec.ts` proving OpenAPI import -> commit -> publish -> live gateway convergence -> live Console HTTP 200 traffic.

## Verification
- `make verify`
- `INTEGRATION_TESTS=1 go test -race -v -run TestOpenAPITransactionalImportAndPartialFailureRollback ./tests/integration/...`
- `cd apps/web && npx playwright test` (3/3 passing against live Docker Compose)
- See `docs/phases/PHASE_12_REPORT.md` for full atomic commit ledger.
