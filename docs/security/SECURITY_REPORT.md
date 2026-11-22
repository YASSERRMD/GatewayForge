# GatewayForge Security Assessment & Gap Report

- **Assessment Date:** 2026-11-22
- **Scope:** Control Plane API, Gateway Data Plane, Next.js Management Studio, PostgreSQL/Redis persistence, Dockerfiles, and GitHub Actions CI.
- **Note on Scope:** This report records verified automated adversarial test outcomes and honest residual limitations. It does not claim formal third-party compliance certification (e.g., SOC 2, ISO 27001, FIPS 140-3).

## 1. Real Defect Found & Remediated During Adversarial Testing
| ID | Severity | Finding | Remediation & Verification |
|---|---|---|---|
| SEC-01 | Medium | `netsec.DefaultTargetPolicy(false)` left `AllowedMockHosts` empty when `allowMocks == false`, causing save-time `ValidateTargetURL("http://mock-upstream:8089", prodPol)` to miss classifying `mock-upstream` as a disabled mock host (connect-time private-IP blocking still protected the gateway, but save-time rejection was skipped). | Fixed in `internal/shared/netsec/target.go` (`62204d61`) so known mock hostnames are always registered in `AllowedMockHosts` and explicitly rejected when `AllowMockUpstreams == false`. Verified by `TestSSRFBlockedDestinationURLsAcrossModes`. |

## 2. Verified Mitigations (Re-Executed Automated Proof)
| Control Area | Acceptance ID | Automated Suite | Verified Result |
|---|---|---|---|
| RBAC negative matrix & cross-tenant IDOR isolation | AT-08 | `tests/security/rbac_tenant_matrix_test.go` | PASS (`401` unauth, `403` missing CSRF, `403` cross-org, `422` cross-tenant IDOR ref) |
| SSRF, IPv6-mapped IPv4, DNS rebinding & redirect blocking | AT-13 | `tests/security/ssrf_rebinding_test.go` | PASS (metadata, private, loopback, schemes, rebind IPs, 302 redirect non-following) |
| Hop-by-hop smuggling, spoofed `X-Forwarded-*`, open proxy, traversal, body limits | AT-02, AT-13 | `tests/security/proxy_abuse_test.go` | PASS (`400` open-proxy/traversal, `404` unmatched, `413` oversized, stripped headers) |
| JWT `alg:none` / `HS256` key confusion, JWKS fail-closed, revoked/expired API keys, log leak scan | AT-06, AT-07, AT-18 | `tests/security/credential_abuse_test.go` | PASS (0 log leaks via `redact.FindLeaks`) |
| Concurrent snapshot swap + key revocation + local token-bucket contention | AT-19 | `tests/security/race_contention_test.go` (`-race`) | PASS (0 data races, exact 25/320 atomic burst ceiling) |
| Redis outage (`fail_open` vs `critical fail_closed`) & control-plane outage LKG + reconciliation | AT-05, AT-10 | `tests/security/outage_chaos_test.go` | PASS (critical routes reject 503 during outage; LKG v1 preserved & auto-converges to v2) |
| Graceful shutdown draining 512 KiB stream + offline LKG restart recovery | AT-05, AT-11 | `tests/security/shutdown_restart_test.go` | PASS (exact SHA-256 stream completion; cold restart serves LKG v1 without control plane) |
| Container & secret hygiene | — | `scripts/check-containers.sh`, `scripts/check-secrets.sh` | PASS (all 4 Dockerfiles multi-stage non-root; 0 tracked `.env` or private keys) |

## 3. Residual Risks, Known Limitations & Planned Remediation
| ID | Severity | Limitation / Residual Risk | Reproduction / Trigger | Planned Remediation |
|---|---|---|---|---|
| RES-01 | Medium | **No built-in TLS/mTLS termination or ACME** inside `cmd/gateway` or `cmd/control-plane`. Local Compose binds HTTP strictly to `127.0.0.1`. | Deploying GatewayForge directly to public networks without a TLS-terminating ingress/LB exposes plaintext traffic and prevents `Secure` cookies unless `GATEWAYFORGE_ENV=production` behind HTTPS. | Deploy behind a TLS-terminating ingress/reverse proxy (Envoy/Nginx/ALB) in production; add native TLS/mTLS listener option in a future release. |
| RES-02 | Low (Runtime) / High (Upstream CVE) | **`postcss <=8.5.22` bundled inside `next@15.5.29`** (and dev-only `vitest@3.2.7` / `tinypool`). | `cd apps/web && npm audit` reports build-time `postcss` and dev-only `vitest`/`tinypool` advisories. Not reachable from untrusted input at runtime; dev deps excluded from standalone Docker image. | Schedule major upgrade to Next.js 16.x and Vitest 5.x once ecosystem peer dependencies stabilize. |
| RES-03 | Low | **Symmetric env-based HMAC key & session secret management** (no external KMS/HSM envelope encryption). | Compromise of host environment variables exposes `SESSION_SECRET` and `GATEWAY_SYNC_TOKEN`. | Document rotation procedures in `docs/ops/ADMIN_RUNBOOK.md` (Phase 15); integrate Vault/KMS secret provider in future roadmap. |
| RES-04 | Low | **In-memory local rate-limit fallback** during Redis outage on `fail_open` routes is per-replica rather than globally coordinated. | During total Redis outage, `standard`/`fail_open` routes allow up to `N * limit` across `N` replicas (by design); `critical` routes enforce `fail_closed` (`503`). | Intentional trade-off documented in `docs/security/THREAT_MODEL.md`; operators classify sensitive routes as `critical`. |
| RES-05 | Low | **`govulncheck` not bundled offline in local toolchain.** | Local `make security-test` runs Go race/security tests and `scripts/check-*`; `govulncheck` requires network access to the Go vulnerability database. | Run `go run golang.org/x/vuln/cmd/govulncheck@latest ./...` in scheduled GitHub Actions workflows. |
