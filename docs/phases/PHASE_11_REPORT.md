# Phase 11 Report — Management UI for Services, Routes, Consumers and Policies

## Summary
Phase 11 delivered complete, schema-validated, RBAC-aware management interfaces for GatewayForge Studio covering Services, Upstream Pools & Targets, Routes, Consumers, One-Time API Keys, and Typed Policies (Auth, Rate-Limit/Quota, Timeout, Retry, Circuit Breaker, CORS) backed by real Control Plane APIs and proven end-to-end with Compose-backed Playwright tests.

## Atomic Commit Ledger
| # | Subject | SHA | Key Verification |
|---|---|---|---|
| 1 | `phase 11: implement service listing` | `5f62a1bd0f22a769352072f7e7649442edd24d31` | `services-view.test.tsx` (search/sort/pagination/RBAC) |
| 2 | `phase 11: implement service editor` | `b74fb97e94b8524673420387a080fd90a18cac08` | `services-manager.test.tsx` (slug/name validation, CSRF, optimistic concurrency) |
| 3 | `phase 11: implement upstream target editor` | `7a6bf895916d46099224864e122d4430445acb24` | `upstreams-manager.test.tsx` (SSRF metadata target rejection & recovery, viewer RBAC) |
| 4 | `phase 11: implement route listing and details` | `1e41065a4cc9cec8de4fe8b3a17cd7ed2a375edb` | `routes-view.test.tsx` (priority, methods, resolved service/upstream, detail modal) |
| 5 | `phase 11: implement route editor` | `07907134e8ef191894074f5bdddeb960aaf29781` | `routes-manager.test.tsx` (live overlap conflict preview & resolution) |
| 6 | `phase 11: implement consumer management` | `4fb878ef6a9b2983b73c6dcd6e88b1418f6a67a4` | `consumers-manager.test.tsx` (create/suspend with If-Match, viewer RBAC) |
| 7 | `phase 11: implement one-time API key UI` | `ca753a005f199073bd430b15e4711d9ae5afa766` | `api-keys-dialog.test.tsx` (shown once, zero storage persistence, wiped on close) |
| 8 | `phase 11: implement auth policy editors` | `fe3d5d53e0a9b5f42ce23750e39735712a7a4131` | `policies-manager.test.tsx` (JWT issuer/audience/JWKS/RS256-ES256 validation) |
| 9 | `phase 11: implement rate limit policy editors` | `ed5fd5aeb7de8e7fd6902acfbdbafb9050a10ab0` | `ratelimit-policy-form.test.tsx` (burst/window bounds & `critical`+`fail_open` guardrail) |
| 10 | `phase 11: implement upstream resilience policy editors` | `fea7d956dabe807bb34a6e4409cb15c5121e9713` | `resilience-policy-form.test.tsx` (non-idempotent POST/PATCH retry ban, timeout/CB/CORS guards) |
| 11 | `phase 11: complete management E2E workflow` | *(HEAD of PR #11)* | `apps/web/e2e/management-workflow.spec.ts` (live Compose Playwright E2E) |

## Live Compose E2E Findings & Verification
- **Strict-JSON Defect Caught & Fixed by Real E2E**: Running `management-workflow.spec.ts` against the live Go Control Plane revealed `POST /services` strictly rejects unknown field `enabled` (services default to enabled on creation and toggle `enabled` via `PATCH`). Fixed `services-manager.tsx` to omit `enabled` on `POST` while retaining it on `PATCH`.
- **Playwright Suite (`cd apps/web && npx playwright test`)**: **2 passed (3.2s)** against live Docker Compose (`postgres`, `redis`, `control-plane`, `gateway`, `gateway-2`, `mock-upstream`, `web`):
  - `dashboard-smoke.spec.ts`: passed (1.1s)
  - `management-workflow.spec.ts`: passed (2.3s) — verified Service -> Upstream Pool + SSRF metadata target rejection -> Route -> Consumer + One-Time API Key (shown once, zero `localStorage` persistence, wiped on dismiss) -> Rate-Limit Policy -> Admin API persistence verification, 0 page errors.
- **Frontend Unit & Lint Gates**: ESLint 0 errors, `tsc --noEmit` 0 errors, Vitest 21 test files / 34 tests passing.
