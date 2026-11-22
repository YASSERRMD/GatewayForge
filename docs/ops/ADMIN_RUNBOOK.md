# GatewayForge Administrator Operations Runbook

## 1. Secret & Credential Rotation

### 1.1 Zero-Downtime `GATEWAY_SYNC_TOKEN` Rotation
`control-plane` accepts a comma-separated list of valid sync tokens (`api.ParseSyncTokens`).
1. Generate a new 32+ byte random token (`openssl rand -hex 32`).
2. Update `control-plane` environment to `GATEWAY_SYNC_TOKEN="<old_token>,<new_token>"` and roll `control-plane` (`docker compose up -d control-plane`).
3. Update `gateway` and `gateway-2` sequentially to `GATEWAY_SYNC_TOKEN="<new_token>"` and verify `/health/ready` returns `200` on each replica.
4. Remove `<old_token>` from `control-plane` (`GATEWAY_SYNC_TOKEN="<new_token>"`) and reload.

### 1.2 Consumer API Key Rotation & Emergency Revocation
- **Atomic Rotation (Zero Downtime / Single-Transaction):** In Studio (`/consumers` → **API Keys** → **Rotate**) or `POST /api/v1/orgs/{orgId}/keys/{keyId}/rotate`. The previous key is revoked and a replacement key is issued in one PostgreSQL transaction; Redis config hints trigger immediate gateway convergence.
- **Immediate Revocation:** `POST /api/v1/orgs/{orgId}/keys/{keyId}/revoke` — revokes key immediately and emits a cache invalidation hint to all gateway replicas.

### 1.3 `SESSION_SECRET` Rotation
- Rotate `SESSION_SECRET` during a planned maintenance window; existing operator sessions will require re-authentication cleanly via `/login`.

## 2. PostgreSQL Backups, Restore & Migration Recovery

### 2.1 Online Logical Backup
```bash
docker compose exec -T postgres pg_dump -U "${POSTGRES_USER:-gatewayforge}" -d "${POSTGRES_DB:-gatewayforge}" -Fc > "gatewayforge-$(date +%Y%m%dT%H%M%S).dump"
```

### 2.2 Restore Procedure
```bash
docker compose stop control-plane
docker compose exec -T postgres pg_restore -U "${POSTGRES_USER:-gatewayforge}" -d "${POSTGRES_DB:-gatewayforge}" --clean --if-exists < gatewayforge-backup.dump
docker compose start control-plane
```
> Gateway replicas continue serving their in-memory/on-disk verified Last-Known-Good (LKG) snapshot uninterrupted throughout PostgreSQL maintenance!

### 2.3 Migration Verification & Checksum Failure Recovery
```bash
DATABASE_URL="postgres://gatewayforge:gatewayforge_local_dev_pw@127.0.0.1:5439/gatewayforge?sslmode=disable" go run ./cmd/migrate status
```
- Migrations are forward-only and SHA-256 checksum verified (`schema_migrations`). Never edit an already-applied `.sql` file; create a new numbered migration instead.
- If an interrupted migration leaves the database dirty, inspect `schema_migrations`, restore from the pre-migration `pg_dump` backup, fix the migration SQL, and re-run `make migrate`.

## 3. Diagnosing Stale or Missing Gateway Replicas
1. Check `/releases` (**Gateway Fleet Convergence**) or query `GET /api/v1/orgs/{orgId}/gateways`.
2. Inspect individual replica health endpoints:
   ```bash
   curl -sS http://127.0.0.1:18081/health/ready
   curl -sS http://127.0.0.1:18081/health/upstreams
   curl -sS http://127.0.0.1:18081/health/ratelimit
   ```
3. **If a replica shows `stale` or `Lagging`:**
   - Check Redis connectivity (`docker compose logs redis gateway`). Even if Redis Pub/Sub hints are missed, periodic reconciliation polls `control-plane` every `GATEWAY_SYNC_INTERVAL_SEC` (default `5s`).
   - Verify `GATEWAY_SYNC_TOKEN` matches `control-plane` and `GET /api/v1/gateway/sync/active` does not return `401`.
   - Check gateway logs for `snapshot checksum mismatch` — gateways reject corrupted payloads and preserve LKG intentionally.

## 4. Emergency Release Rollback
### Via Studio UI (Recommended)
1. Open `/releases` as `admin` or `owner`.
2. Locate the last known healthy immutable version (e.g. `v1`) under **Immutable Release History**.
3. Click **Rollback to vN**, enter an audit reason, and confirm.
4. Observe **Gateway Fleet Convergence** until all replicas report `Converged` on the restored checksum.
5. Verify the `publication.rollback` audit event under `/insights`.

### Via Control Plane API
```bash
curl -sS -X POST "http://127.0.0.1:18080/api/v1/orgs/${ORG_ID}/publications/${TARGET_VERSION}/activate" \
  -b cookies.txt \
  -H "X-CSRF-Token: ${CSRF_TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{"reason":"Emergency incident rollback"}'
```

## 5. Redis Outage Handling
- Routes with `security_class: "standard"` and `fail_mode: "fail_open"` fall back automatically to bounded local per-replica rate limiting (`DegradedOpen=true`) and emit metric `gatewayforge_ratelimit_events_total{outcome="degraded_open"}`.
- Routes with `security_class: "critical"` strictly enforce `fail_closed` and return `503 Service Unavailable` until Redis recovers.
- Recovery is automatic with zero gateway restarts once Redis becomes reachable again.
