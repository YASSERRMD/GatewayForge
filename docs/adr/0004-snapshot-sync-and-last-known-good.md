# ADR 0004 — Immutable Snapshot Synchronization and Last-Known-Good (LKG) Protection

- **Status:** Accepted
- **Date:** 2026-11-22

## Context

GatewayForge separates the management control plane from the stateless HTTP reverse-proxy data plane. Multiple gateway replicas must converge onto newly published routing and policy configurations without restarting, without dropping active requests, and without failing closed into an empty routing table when PostgreSQL, Redis, or the control plane experiences transient outages.

## Decision

1. **Immutable, Checksummed Snapshots:**
   - Every publication (`POST /api/v1/orgs/{orgId}/publications`) compiles the organization's services, upstream pools, targets, routes, consumer API key hashes, and attached policies into a deterministic JSON snapshot (`internal/shared/snapshot`), hashes it with SHA-256, and stores it immutably in PostgreSQL.
   - Activating or rolling back a release (`POST /api/v1/orgs/{orgId}/publications/{version}/activate`) updates the active pointer under optimistic concurrency and publishes a lightweight invalidation hint on Redis Pub/Sub (`gatewayforge:config:changed`).
2. **Pull-Based Convergence with ETag Caching:**
   - Gateways fetch the active compiled snapshot over HTTP from `GET /api/v1/gateway/snapshots/active` authenticated with `Authorization: Bearer <GATEWAY_SYNC_TOKEN>` (supporting comma-separated overlap tokens during rotation) and `If-None-Match: "<checksum>"`.
   - Redis Pub/Sub accelerates convergence to sub-second latency; periodic polling (`GATEWAY_SYNC_INTERVAL_SEC`, default `5s`) guarantees convergence even when Pub/Sub notifications are missed or Redis restarts.
3. **Atomic In-Memory Swap and On-Disk LKG Fallback:**
   - Each gateway validates the downloaded snapshot and checksum before compiling a fresh `matcher.Matcher`, upstream pool table, and policy chains, then swaps the active runtime pointer atomically via `atomic.Pointer` (`internal/gateway/table`).
   - Invalid or corrupted snapshots are rejected before swap; the previous active routing table remains untouched and the sync error is surfaced on `/orgs/{orgId}/gateways` and Prometheus metrics.
   - Successful snapshots are persisted to local disk (`GATEWAY_LKG_PATH`) via atomic temp-file rename so a gateway replica restarting while the control plane is unreachable boots directly into its last-known-good configuration.

## Consequences

- **Positive:** Zero-downtime reloads, lock-free request-path reads, deterministic convergence across replicas, and verified survival of Redis and control-plane outages.
- **Trade-off:** Gateways are eventually consistent during the sub-second Pub/Sub window (or up to `GATEWAY_SYNC_INTERVAL_SEC` during Redis outage), which is explicitly surfaced via fleet convergence telemetry.
