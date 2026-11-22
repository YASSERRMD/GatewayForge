# Phase 08: Resilience, Health and Upstream Failure Behavior

## Summary
Implements active target health probing with bounded timeouts and jitter (never probing/routing to administratively disabled targets), passive upstream failure streak tracking (excluding business 4xx and client cancellations), health-aware weighted target failover and all-down 503 handling, strict idempotent retry guarding (forbidding automatic `POST`/`PATCH` retries and capping replay body buffers), bounded exponential backoff with jitter and deadline budget checks, closed/open/half-open circuit breakers with bounded half-open concurrency, distinct per-route connect vs total timeouts, fail-fast per-route bulkhead protection, bounded graceful connection draining under load, role-aware safe health diagnostics APIs (`/health/upstreams` and `/api/v1/orgs/{orgId}/upstreams/health`), and a two-replica fault-injection E2E suite.

## Tasks Implemented (11 Atomic Commits)
1. `phase 08: implement active target health checks`
2. `phase 08: implement passive failure tracking`
3. `phase 08: implement target health selection`
4. `phase 08: implement idempotent retry guard`
5. `phase 08: implement bounded exponential backoff`
6. `phase 08: implement circuit breaker`
7. `phase 08: implement per-route timeouts`
8. `phase 08: implement bulkhead protection`
9. `phase 08: implement connection draining`
10. `phase 08: expose health state diagnostics`
11. `phase 08: run fault-injection E2E`

## Verification
- `make verify` — PASS
- `make integration` — PASS (including `TestTwoReplicaFaultInjectionResilienceE2E` & `TestUpstreamHealthDiagnosticsRoleVisibilityAndSafety`)
