# GatewayForge Threat Model and Trust Boundaries

## 1. System Scope & Protected Assets
GatewayForge is a self-hosted, multi-tenant API gateway and management studio consisting of:
- **Studio UI (`apps/web`, Next.js 15)** — browser SPA + runtime same-origin API proxy (`/api/v1/[...path]`).
- **Control Plane (`cmd/control-plane`)** — tenant-scoped RBAC REST API, draft compiler, immutable snapshot publisher, safe developer console proxy, and OpenAPI parser/importer.
- **Gateway Data Plane (`cmd/gateway`, multi-replica)** — high-throughput reverse proxy, auth/JWT/CORS/rate-limit/resilience enforcer, snapshot syncer, Prometheus `/metrics`, and OTEL tracing.
- **State Stores (`PostgreSQL 16`, `Redis 7`)** — canonical tenant config, append-only audit logs, Argon2id user hashes, HMAC-SHA256 API key hashes, distributed rate-limit buckets, and gateway heartbeats.

### High-Value Assets
| Asset | Sensitivity | Storage & Handling Rule |
|---|---|---|
| Operator passwords | Critical | Argon2id salted hashes only (`internal/control/auth/argon2.go`); never logged. |
| Session cookies (`gwf_session`) | Critical | `HttpOnly`, `SameSite=Lax`, `Secure` in production; SHA-256 hashed in PostgreSQL `sessions` table; paired with `X-CSRF-Token`. |
| Consumer API keys (`gwf_<prefix>.<secret>`) | Critical | Shown once in `Cache-Control: no-store` response & ephemeral React state; stored exclusively as keyed HMAC-SHA256 hashes (`internal/shared/apikey`); compared in constant time. |
| Gateway sync token (`GATEWAY_SYNC_TOKEN`) | High | Shared bearer token restricted to `/api/v1/gateway/sync/*`; supports zero-downtime comma-separated rotation. |
| Immutable release snapshots | High | SHA-256 checksummed (`snapshot.VerifyChecksum()`); enforced immutable by PostgreSQL triggers (`enforce_publications_immutable`). |
| Append-only audit events | High | Enforced append-only by PostgreSQL triggers (`enforce_audit_events_append_only`); deeply redacted before persistence. |

## 2. Trust Boundaries
1. **TB-1: Untrusted Internet / Browser → Next.js Studio & Control Plane API**
   - Unauthenticated clients can only hit `/health/*`, `/api/v1/auth/bootstrap` (one-time guarded), and `/api/v1/auth/login`.
   - All state-changing API calls require valid session cookie + matching `X-CSRF-Token` + `RequireOrgContext` membership + `RequirePermission` RBAC + `DecodeStrictJSON` body cap.
2. **TB-2: Tenant A ↔ Tenant B (Multi-Tenant Isolation)**
   - Every org-scoped query and mutation binds `org_id = orgCtx.OrgID` derived strictly from verified membership, preventing IDOR and cross-tenant resource references (`service_id`, `upstream_pool_id`, `route_id`, `consumer_id`, `policy_id`).
3. **TB-3: Untrusted API Clients → Gateway Data Plane**
   - Explicit allowlist routing only (unknown hosts/paths return `404` without upstream dial; absolute open-proxy URIs rejected `400`).
   - Hop-by-hop headers stripped; `X-Forwarded-*` stripped from untrusted peers; request body & header limits enforced.
4. **TB-4: Gateway Data Plane / Control Plane → Upstream Networks & JWKS**
   - Both save-time (`netsec.ValidateTargetURL`) and connect-time (`proxy.NewSafeTransport` dialing validated resolved IPs directly) block cloud metadata (`169.254.169.254`, `fd00:ec2::254`, `metadata.google.internal`), link-local, loopback, RFC1918 private ranges, IPv6-mapped IPv4 (`::ffff:169.254.169.254`), and automatic redirect following.
   - Developer Request Console (`POST /console/execute`) anchors strictly to server-configured `GATEWAY_PROXY_URL` and never accepts arbitrary destination URLs.
   - OpenAPI parser (`internal/control/openapi/parser.go`) caps upload at 2 MiB and rejects all external `$ref` pointers (`http://`, `file://`, relative file paths).
5. **TB-5: Control Plane ↔ Gateway Replicas**
   - Read-only snapshot distribution via authenticated `/api/v1/gateway/sync/active`; gateways verify SHA-256 checksums before atomic pointer swap and preserve last-known-good on any corruption or outage.

## 3. Threat Matrix & Entry-Point Checklist
| Entry Point | Threat | Mitigation | Automated Proof |
|---|---|---|---|
| Control Plane Auth & Sessions | Credential stuffing, CSRF, session fixation | Argon2id, HttpOnly cookie, per-session CSRF token, strict JSON decoding | `tests/security/rbac_tenant_matrix_test.go`, `tests/integration/auth_context_test.go` |
| Control Plane Org APIs | IDOR, privilege escalation (`viewer`/`developer` -> publish/rollback/owner) | Deny-by-default RBAC matrix (`internal/control/rbac`), tenant-bound SQL `WHERE org_id = $1` | `tests/security/rbac_tenant_matrix_test.go`, `tests/integration/publish_rbac_boundaries_test.go` |
| Upstream Pool / JWKS / Console / OpenAPI | SSRF, DNS rebinding, redirect to IMDSv1, external `$ref` LFI | Save-time + connect-time IP validation, disabled redirects, anchored console transport, local-only `#/` `$ref` | `tests/security/ssrf_rebinding_test.go`, `internal/control/api/console_handlers_test.go`, `internal/control/openapi/parser_test.go` |
| Gateway Proxy Pipeline | Request smuggling, hop-by-hop abuse, spoofed `X-Forwarded-*`, open proxy, path traversal | RFC hop-by-hop strip, untrusted forwarded header strip, origin-form enforcement, body/header caps | `tests/security/proxy_abuse_test.go` |
| Consumer Auth (API Keys & JWT) | `alg:none`, `HS256` key confusion, revoked/expired key reuse, JWKS outage bypass, log secret leaks | Asymmetric `RS256`/`ES256` allowlist, strict `iss`/`aud`/`exp`, fail-closed JWKS, constant-time HMAC check, `redact.NewJSONLogger` | `tests/security/credential_abuse_test.go` |
| Distributed Rate Limits & Quotas | Race bypass, Redis outage bypass on critical routes | Atomic Redis Lua token bucket, explicit `fail_closed` requirement for `security_class=critical` | `tests/security/race_contention_test.go`, `tests/security/outage_chaos_test.go` |
| Studio Web UI (`/console`, `/consumers`, `/insights`) | Stored/reflected XSS from upstream HTML, secret persistence in `localStorage` | React text escaping (zero `dangerouslySetInnerHTML`), safe `.txt` download, ephemeral one-time API key state, sanitized opt-in history, deep audit redaction | `response-panel.test.tsx`, `api-keys-dialog.test.tsx`, `console-history.test.ts`, `audit-explorer.test.tsx` |
| CI/CD & Supply Chain | Over-privileged `GITHUB_TOKEN`, secret leaks in repo, unpinned tools | Minimal `permissions: contents: read`, pinned `golangci-lint`, automated secret scan & security test job | `.github/workflows/ci.yml`, `docs/security/CI_HARDENING.md` |
