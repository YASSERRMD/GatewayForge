# Phase 06: Consumers, API Keys and JWT Policy

## Summary
Implements end-to-end consumer management, one-time hashed API keys (issuance, revocation, atomic rotation), gateway constant-time API-key authentication, scope and policy attachment precedence (`route > service > org`), strict asymmetric RS256/ES256 JWT verification with a bounded SSRF-safe JWKS cache, CORS preflight/response enforcement denying wildcard+credentials misuse, sensitive header/log redaction, and two-replica E2E credential verification.

## Tasks Implemented (11 Atomic Commits)
1. `phase 06: implement consumer CRUD`
2. `phase 06: implement one-time API-key issuance`
3. `phase 06: implement API-key revoke and rotate`
4. `phase 06: implement gateway key authentication`
5. `phase 06: implement scopes and policy attachments`
6. `phase 06: implement JWT signature verification`
7. `phase 06: implement bounded JWKS cache`
8. `phase 06: implement auth policy precedence`
9. `phase 06: implement CORS policy`
10. `phase 06: implement sensitive header redaction`
11. `phase 06: add end-to-end credential scenarios`

## Verification
- `make verify` — PASS
- `make integration` — PASS (two-replica credential E2E + key lifecycle + consumer CRUD + convergence + DB schema/auth/proxy suites)

See `docs/phases/PHASE_06_REPORT.md` for full task-to-commit and verification evidence.
