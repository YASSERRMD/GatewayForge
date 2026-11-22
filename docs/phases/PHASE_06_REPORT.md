# Phase 06 Report — Consumers, API Keys and JWT Policy

- **Branch:** `phase/06-consumer-security`
- **PR:** Pending PR creation
- **Merge commit:** Pending merge
- **Date:** 2026-11-22

## Completed Tasks (01–11)

1. `ca6ad84a084c7b296e4d84072162db5edb1d305e` — `phase 06: implement consumer CRUD`
2. `b814245f2ee3ae3fe53e793b027ec01dd93f8035` — `phase 06: implement one-time API-key issuance`
3. `7e948ebc0a457f53c1792b842ef46ffbc175235b` — `phase 06: implement API-key revoke and rotate`
4. `9660b9f6a1e48ea39f413a5592f6a93723051c` — `phase 06: implement gateway key authentication`
5. `efd2011d76a1e48ea39f413a5592f6a93723051c` — `phase 06: implement scopes and policy attachments`
6. `1ccdb24a15e105060286c29c83d77c26ba01560f` — `phase 06: implement JWT signature verification`
7. `c7fddff2617ee0379236a080bc0eb5f13e8b8946` — `phase 06: implement bounded JWKS cache`
8. `c53b9aae82df2d01c6c66e7d30559e8774954fed` — `phase 06: implement auth policy precedence`
9. `a15512eeb598f3e9568605580befb0fb290bdc4e` — `phase 06: implement CORS policy`
10. `97e21e807717fb52769446ef9cccdb677c7edb3c` — `phase 06: implement sensitive header redaction`
11. *(HEAD commit of this PR)* — `phase 06: add end-to-end credential scenarios`

## Verification Evidence

- `make verify`: PASS (`go fmt`, `go vet`, `golangci-lint` 0 issues, ESLint, `tsc --noEmit`, `go test -race ./...`, Vitest, Go/Web builds, `docker compose config --quiet`).
- `make integration`: PASS including `TestConsumerSecurityTwoReplicaE2E` proving across two synced gateway replicas targeting live Compose `mock-upstream`:
  - Anonymous traffic succeeds on public routes (`200`) and fails uniformly (`401`) on API-key/JWT-protected routes.
  - Valid scoped API key succeeds (`200`) and `X-Api-Key` is stripped before reaching upstream.
  - Wrong-scope API key returns `403`, expired API key returns `401`.
  - Key rotation atomically revokes the old key and issues a replacement; both gateway replicas converge via Redis invalidation hints (`old=401`, `new=200`).
  - Key revocation propagates to both gateway replicas (`401`).
  - Valid RS256 JWT succeeds (`200`) with `Authorization` stripped before upstream; tampered signature, wrong audience, and `alg:none` JWTs all return `401`.
  - CORS preflight `OPTIONS` succeeds (`204`) before auth for allowlisted origins/methods/headers, denies unlisted origins (`403`), and strictly rejects wildcard `*` with `allow_credentials=true`.
  - Automated log-redaction scan (`redact.FindLeaks`) confirms zero plaintext API keys or JWTs appear in captured structured logs.

## Security Controls Verified

- API keys are never stored in plaintext (only prefix + keyed HMAC-SHA256 hash) and are returned once on issuance/rotation with `Cache-Control: no-store`.
- Gateway API-key verification executes constant-time HMAC comparison against either the real hash or a dummy hash to avoid prefix-enumeration timing oracles.
- JWT verifier strictly enforces `RS256`/`ES256`, rejects `alg: none` and `HS256` confusion, validates `iss`/`aud`/`exp`/`nbf` and P-256 curve membership (`crypto/ecdh`), and fetches JWKS through an SSRF-validated, no-redirect, size/TTL/cooldown-bounded cache.

## Confirmation

- No secrets committed, 11 atomic commits on `phase/06-consumer-security`, clean working tree.
