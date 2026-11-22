# Phase 14 — Security Hardening, Concurrency Audit and Chaos Tests

## Summary
Delivers adversarial security regression suites, chaos & outage resilience proofs, honest dependency/container audits, hardened CI with secret scanning, and a transparent security gap report.

## Key Changes
- **Threat Model (`docs/security/THREAT_MODEL.md`)**: Documents assets, trust boundaries TB-1..TB-5, and API/Gateway/UI entry-point threat mitigations.
- **Adversarial Security & Chaos Suite (`tests/security/`)**:
  - `rbac_tenant_matrix_test.go`: Negative RBAC matrix + live PostgreSQL cross-tenant isolation & IDOR injection tests (`AT-08`).
  - `ssrf_rebinding_test.go`: Save-time & connect-time SSRF, IPv6-mapped IPv4, DNS rebinding, and non-redirect tests (`AT-13`) — caught and fixed `DefaultTargetPolicy(false)` mock-host validation gap.
  - `proxy_abuse_test.go`: Open-proxy URI, hop-by-hop smuggling, spoofed `X-Forwarded-*`, encoded traversal, and body limit abuse tests (`AT-02`, `AT-13`).
  - `credential_abuse_test.go`: JWT `alg:none` & `HS256` key-confusion attacks, JWKS outage fail-closed, expired/revoked/suspended API keys, and automated operator log redaction scan (`AT-06`, `AT-07`, `AT-18`).
  - `race_contention_test.go`: High-concurrency config swap + key revocation + cancellation + atomic rate-limit ceiling under `-race` (`AT-19`).
  - `outage_chaos_test.go` & `shutdown_restart_test.go`: Redis `fail_open` vs `critical fail_closed` recovery, control-plane outage LKG preservation & periodic reconciliation, 512 KiB graceful stream drain, and offline LKG restart (`AT-05`, `AT-10`).
- **Audits & CI Hardening**: `docs/security/DEPENDENCY_AUDIT.md`, `scripts/check-containers.sh`, `scripts/check-secrets.sh`, hardened `.github/workflows/ci.yml` security job, `docs/security/CI_HARDENING.md`, `make security-test`, and honest `docs/security/SECURITY_REPORT.md`.

## Verification
- `make security-test`
- `INTEGRATION_TESTS=1 go test -race -v ./tests/security/...`
- `make verify`
- See `docs/phases/PHASE_14_REPORT.md` for full atomic commit ledger.
