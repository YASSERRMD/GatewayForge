# Phase 11 — Management UI for Services, Routes, Consumers and Policies

## Summary
Implements complete operator management views and schema-validated editors across Services, Upstreams, Routes, Consumers, One-Time API Keys, and Typed Policies (`api_key`, `jwt`, `rate_limit`, `quota`, `timeout`, `retry`, `circuit_breaker`, `cors`) in GatewayForge Studio.

## Key Changes
- **Services (`/services`)**: Server-side search, sort, pagination, optimistic concurrency (`If-Match: W/"rev-N"`), RBAC-gated create/edit/delete modals.
- **Upstreams (`/upstreams`)**: Pool and weighted target editor merged with safe `/upstreams/health` diagnostics and clear server-side SSRF rejection display.
- **Routes (`/routes`)**: Precedence-ordered route table resolving service & pool attachments, detail modal, method checkboxes, and live client-side overlap conflict preview.
- **Consumers & One-Time API Keys (`/consumers`)**: Consumer CRUD + suspend/activate lifecycle, plus guarded `ApiKeysDialog` showing plaintext `gwf_*` credentials exactly once in ephemeral memory (zero storage persistence, wiped on dismiss/close) with rotate & revoke actions.
- **Typed Policies (`/policies`)**: Typed forms with domain guardrails (`RS256`/`ES256`-only JWT, `critical`+`fail_open` rate-limit rejection, non-idempotent `POST`/`PATCH` retry exclusion warning, CORS wildcard+credentials rejection) and scope attachment management (`org`/`service`/`route`).
- **Live Compose E2E**: `apps/web/e2e/management-workflow.spec.ts` proving full Service -> Upstream (w/ SSRF check) -> Route -> Consumer + One-Time Key -> Policy -> Control Plane Admin API persistence.

## Verification
- `make verify` (Go vet/test/race/golangci-lint + Web ESLint/tsc/Vitest/Next build)
- `cd apps/web && npx playwright test` (2/2 specs passing in 3.2s against live Docker Compose)
- See `docs/phases/PHASE_11_REPORT.md` for full atomic commit ledger.
