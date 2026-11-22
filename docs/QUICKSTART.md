# GatewayForge Installation & Clean-Room Quickstart Guide

This guide walks through a complete clean-room installation from a fresh clone to live proxied traffic through two converged GatewayForge data-plane replicas.

## 1. Prerequisites
- **Docker** (`>= 24.x`) & **Docker Compose v2** (`docker compose version`)
- **Go** (`1.25+`) — for running migrations, seed CLI, and Go tests locally
- **Node.js** (`24.x`) & **npm** (`11+`) — for frontend development and Playwright E2E tests
- **curl** & **make**

> All Docker Compose host ports bind strictly to `127.0.0.1` on non-conflicting ports (`3000`, `5439`, `6389`, `18080`, `18081`, `18082`, `18089`).

## 2. Fresh-Clone Setup
```bash
git clone https://github.com/YASSERRMD/GatewayForge.git
cd GatewayForge
cp .env.example .env
```

Review `.env` and replace placeholder secrets (`SESSION_SECRET`, `GATEWAY_SYNC_TOKEN`, `POSTGRES_PASSWORD`, `REDIS_PASSWORD`) if deploying beyond localhost.

## 3. Start Stack, Apply Migrations & Seed Demo Data
```bash
make dev
```
This runs:
1. `docker compose up -d --build --wait` — builds and starts `postgres`, `redis`, `mock-upstream`, `control-plane`, `gateway`, `gateway-2`, and `web`.
2. `make migrate` — applies all embedded SQL migrations in `migrations/`.
3. `make seed` — idempotently seeds demo organizations, `echo-api` service, `mock-upstream` pool, `/echo` route, demo consumer, and initial active release `v1` (zero production secrets).

Optional observability stack (Prometheus `:19090` + Jaeger `:16686`):
```bash
make dev-observability
```

## 4. Bootstrap First Administrator (Interactive Studio or CLI)
Open **GatewayForge Studio** at `http://127.0.0.1:3000/login` (or bootstrap via `curl`):
```bash
curl -sS -X POST http://127.0.0.1:18080/api/v1/auth/bootstrap \
  -H 'Content-Type: application/json' \
  -d '{
    "email": "admin@gatewayforge.local",
    "password": "GatewayForge!Admin2026",
    "display_name": "Studio Admin",
    "org_name": "GatewayForge Demo",
    "org_slug": "gatewayforge-demo"
  }'
```
Expected status: `201 Created` on first bootstrap (`409 Conflict` once already initialized).

## 5. Verify Live Traffic Through Both Gateway Replicas
After publishing a route (or using the seeded `demo.gatewayforge.local` `/echo` route):

### Matched Route (`HTTP 200`)
```bash
curl -i http://127.0.0.1:18081/echo?hello=world -H 'Host: demo.gatewayforge.local'
curl -i http://127.0.0.1:18082/echo?hello=world -H 'Host: demo.gatewayforge.local'
```
Expected response on **both** replicas:
- `HTTP/1.1 200 OK`
- `X-Request-Id: req_...`
- JSON body echoed from `mock-upstream-1` containing `"path":"/echo"` and `"query":{"hello":["world"]}`.

### Unknown / Unmatched Route (`HTTP 404`, No Open Proxy)
```bash
curl -i http://127.0.0.1:18081/does-not-exist -H 'Host: unknown.local'
```
Expected response:
- `HTTP/1.1 404 Not Found`
- JSON RFC-7807 error payload with `"code":"route_not_found"` and correlation `X-Request-Id`.

## 6. Clean-Room Verification Checklist
- [ ] `docker compose ps` shows all 7 core services `(healthy)`
- [ ] `curl -sf http://127.0.0.1:18080/health/ready` returns `{"status":"ready",...}`
- [ ] `curl -sf http://127.0.0.1:18081/health/ready` and `:18082/health/ready` return `ready`
- [ ] Studio loads at `http://127.0.0.1:3000` and authenticates via `/login`
- [ ] `make verify`, `make security-test`, `make integration`, and `make e2e` pass
