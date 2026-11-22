# ADR-0003: Control-Plane Session and CSRF Protection Model

- **Status:** Accepted
- **Date:** 2026-11-22

## Decision

1. **Opaque server-side sessions:** On successful login (`POST /api/v1/auth/login`), the control plane generates 32 bytes of cryptographically random entropy (hex-encoded) for the session cookie `gwf_session` and stores only its SHA-256 hash in PostgreSQL `sessions.token_hash`.
2. **Cookie attributes:** `HttpOnly=true`, `SameSite=Lax`, `Path=/`, `Max-Age=28800` (8 hours default), and `Secure=true` in production (or whenever served over TLS).
3. **Server-side revocation:** `POST /api/v1/auth/logout` sets `sessions.revoked_at = NOW()` in PostgreSQL and expires cookies immediately. Replaying a previously valid cookie after logout fails with `401 Unauthorized`.
4. **CSRF defense on state-changing requests:** Each session carries a cryptographically random 32-byte `csrf_token` stored server-side and mirrored in a non-HttpOnly `gwf_csrf` cookie (`SameSite=Lax`). Every authenticated `POST`, `PUT`, `PATCH`, or `DELETE` request MUST send the matching token in the `X-CSRF-Token` request header; the server verifies it in constant time against the session record in PostgreSQL.
5. **No default credentials:** Initial admin creation occurs exclusively through dev/test-only one-time `POST /api/v1/auth/bootstrap` with operator-supplied credentials (>= 12 chars), disabled in production.
