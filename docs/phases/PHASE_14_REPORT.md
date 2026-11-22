# Phase 14 Report — Security Hardening, Concurrency Audit and Chaos Tests

## Summary
Phase 14 established a documented threat model, built an automated adversarial security & chaos test suite (`tests/security/`), remediated a real save-time mock-host policy bug discovered during adversarial testing, performed honest dependency & container audits, hardened GitHub Actions CI with least-privilege scopes and a dedicated security job, and published an objective security gap report.

## Atomic Commit Ledger
| # | Subject | SHA | Key Verification |
|---|---|---|---|
| 1 | `phase 14: document threat model and trust boundaries` | `d9a5e34717d1b227f178c696fb59434fdc81000f` | `docs/security/THREAT_MODEL.md` (assets, TB-1..TB-5, API/Gateway/UI entry-point checklist) |
| 2 | `phase 14: run authorization and tenant leakage suite` | `ea252152493cc9ffb31cabb9713783eca683ba16` | `TestRBACNegativePermissionMatrix` + live PG `TestCrossTenantIsolationAndIDORMatrix` (`AT-08`) |
| 3 | `phase 14: run SSRF and DNS rebinding suite` | `62204d61ff4b5902618dd225b3fb306babfd75ee` | Caught & fixed `DefaultTargetPolicy(false)` mock-host bypass + `TestSSRF*`/`TestConnectTimeDNSRebinding*` (`AT-13`) |
| 4 | `phase 14: run proxy protocol abuse suite` | `6ac9cb21c46c41d53fa751465f8f2bdd45502f25` | `TestProxyProtocolAbuseSmugglingSpoofingAndLimits` (`AT-02`, `AT-13`) |
| 5 | `phase 14: run JWT and credential abuse suite` | `88c49359a7d9557f6821400be746ae21c482ad5a` | `TestJWTAlgorithmSubstitution*` + `TestAPIKeyRevokedExpiredSuspendedAndLogRedaction` (`AT-06`, `AT-07`, `AT-18`) |
| 6 | `phase 14: run race and contention suite` | `249a227637fca53a7d0a05707c21bacc83d2de7c` | `TestConcurrentConfigSwapKeyRevocationAndProxyTrafficRaceFree` + `TestRateLimiterHighContentionAtomicCeiling` (`AT-19`) |
| 7 | `phase 14: run database/Redis outage suite` | `0f8fbd77c93d2b3b647f72d68428ba0eb3797c05` | `TestRedisOutageFailOpenVsFailClosedAndRecovery` + `TestControlPlaneOutagePreservesLKGAndPeriodicReconciliationRecovers` (`AT-05`, `AT-10`) |
| 8 | `phase 14: run graceful shutdown and restart suite` | `b9f0cd0e587d82fdf93341d83c839f8818d75e56` | `TestGracefulShutdownDrainsLargeStreamAndRestoresLKGOnRestart` (512 KiB stream drain + offline LKG restart) |
| 9 | `phase 14: run dependency and container checks` | `e9565ede68048268422f97138d706b7dd4f9739d` | `docs/security/DEPENDENCY_AUDIT.md` (measured `npm audit`, Go modules) + `scripts/check-containers.sh` |
| 10 | `phase 14: harden CI and repo protection guidance` | `4bf364b7949d62301f019df601386c910ac86261` | `.github/workflows/ci.yml` security job + `scripts/check-secrets.sh` + `docs/security/CI_HARDENING.md` |
| 11 | `phase 14: publish security gap report` | *(HEAD of PR #14)* | `make security-test`, `docs/security/SECURITY_REPORT.md`, full `make verify` |

## Verification Summary
- `make security-test`: PASSED (`check-containers.sh`, `check-secrets.sh`, all tests in `./tests/security/...` with `-race`).
- `INTEGRATION_TESTS=1 go test -race -v ./tests/security/...`: PASSED including live PostgreSQL cross-tenant IDOR matrix.
- `make verify`: PASSED cleanly across Go, Web, and Docker Compose.
