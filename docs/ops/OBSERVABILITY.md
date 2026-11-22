# GatewayForge Observability Runbook

This runbook documents GatewayForge's logging, distributed tracing, Prometheus metrics, gateway convergence heartbeats, tenant-isolated aggregate APIs, cardinality safeguards, and recommended alert thresholds.

## 1. Local Observability Stack

Start GatewayForge with the optional local Prometheus and Jaeger (OTLP) stack:

```bash
OTEL_EXPORTER_OTLP_ENDPOINT=http://jaeger:4318 \
TRACE_VIEWER_BASE_URL=http://localhost:16686/trace \
docker compose --profile observability up -d --build
```

Local endpoints (bound to `127.0.0.1` only):
- Gateway 1 Prometheus scrape: `http://127.0.0.1:18081/metrics`
- Gateway 2 Prometheus scrape: `http://127.0.0.1:18082/metrics`
- Gateway 1 upstream pool health (safe aggregated): `http://127.0.0.1:18081/health/upstreams`
- Gateway 1 rate-limit telemetry: `http://127.0.0.1:18081/health/ratelimit`
- Prometheus UI: `http://127.0.0.1:19090`
- Jaeger UI: `http://127.0.0.1:16686`

## 2. Prometheus Metrics Reference

| Metric | Type | Bounded Labels | Description |
|---|---|---|---|
| `gatewayforge_requests_total` | Counter | `route_id`, `service_id`, `method`, `status_class`, `outcome` | Total HTTP requests processed by the gateway data plane |
| `gatewayforge_request_duration_seconds` | Histogram | `route_id`, `service_id`, `le` | Request latency histogram (`0.005s` to `10s`, `+Inf`) |
| `gatewayforge_requests_in_flight` | Gauge | *(none)* | Current number of in-flight proxy requests |
| `gatewayforge_auth_failures_total` | Counter | `route_id`, `status` | Authentication (`401`) and scope authorization (`403`) denials |
| `gatewayforge_upstream_failures_total` | Counter | `route_id`, `service_id`, `status` | Upstream/gateway `5xx` responses (`502`, `503`, `504`) |
| `gatewayforge_active_config_version` | Gauge | *(none)* | Currently loaded immutable published snapshot version |
| `gatewayforge_ratelimit_decisions_total` | Counter | `route_id`, `outcome`, `backend` | Rate-limit & quota decisions (`allowed`, `denied`, `allowed_fail_open`, `denied_fail_closed`, `error`) |

### Strict Cardinality Controls

- Allowed labels are strictly limited to `route_id`, `service_id`, `method`, `status_class`, `status`, `outcome`, `backend`, and `le`.
- High-cardinality and sensitive identifiers (`api_key`, `token`, `jwt`, `client_ip`, `raw_path`, `query`, `request_id`, `trace_id`, `user_id`, `email`) are **prohibited** as metric labels.
- Distinct `route_id` (default max 256) and `service_id` (default max 128) series are capped; overflow or invalid values fold deterministically into `__overflow__`.

## 3. Structured JSON Logging & Secret Redaction

- Both `control-plane` and `gateway` emit JSON logs via `internal/shared/redact.NewJSONLogger` and `internal/shared/logging.LogAccess`.
- Standardized fields: `ts`, `level`, `service`, `env`, `request_id`, `trace_id`, `org_id`, `route_id`, `service_id`, `method`, `route_prefix`, `status`, `outcome`, `duration_ms`.
- Request/response bodies, query strings, `Authorization`, `Cookie`, `X-Api-Key`, `X-Csrf-Token`, full `gwf_*` API keys, and JWT `eyJ...` tokens are automatically scrubbed to `[REDACTED]`.

## 4. Distributed Tracing & Request Correlation

- W3C `traceparent` is extracted on inbound gateway requests (or generated safely if absent/malformed) and propagated to upstream requests.
- Pipeline spans emitted per request:
  1. `gateway.inbound` (server span)
  2. `gateway.policy.auth`
  3. `gateway.policy.ratelimit`
  4. `gateway.upstream.proxy` (client span)
- Sampling defaults to `1.0` in development/test and `0.1` in production (`ParentBased(TraceIDRatioBased)`).
- Control-plane lookup endpoint:
  - `GET /api/v1/orgs/{orgId}/telemetry/traces/{requestId}` (requires `metrics:read`, strictly tenant-isolated).
  - Returns a validated Jaeger/viewer URL when `TRACE_VIEWER_BASE_URL` is configured, or explicit `trace_viewer_available: false` with a clear reason otherwise.

## 5. Heartbeats, Retention & Control-Plane Insight APIs

- **Gateway heartbeats (`GET /api/v1/orgs/{orgId}/gateways`)**:
  - Gateways publish every `10s` to Redis (`gwf:gw:hb:<instance_id>`, TTL `90s`).
  - Liveness classification avoids false outage alarms from a single missed beat:
    - `<= 30s`: `healthy`
    - `31s..60s`: `stale`
    - `> 60s`: `offline`
- **Traffic aggregates & trends (`GET /api/v1/orgs/{orgId}/metrics/summary|timeseries|errors`)**:
  - Minute-bucket Redis rollups scoped strictly by `org_id` with bounded `24h` default TTL retention.
  - Supported timeframes: `15m` (1m buckets), `1h` (5m buckets), `6h` (15m buckets), `24h` (60m buckets).

## 6. Recommended Alert Thresholds (PromQL)

```promql
# 1. Sustained 5xx upstream error rate > 2% over 5 minutes
sum(rate(gatewayforge_upstream_failures_total[5m]))
/ clamp_min(sum(rate(gatewayforge_requests_total[5m])), 1) > 0.02

# 2. p95 gateway latency > 500ms over 5 minutes
histogram_quantile(0.95, sum(rate(gatewayforge_request_duration_seconds_bucket[5m])) by (le)) > 0.5

# 3. Gateway configuration version divergence across replicas for > 2 minutes
(max(gatewayforge_active_config_version) - min(gatewayforge_active_config_version)) > 0

# 4. Critical rate-limiter fail-closed denials during Redis outage
sum(rate(gatewayforge_ratelimit_decisions_total{outcome="denied_fail_closed"}[2m])) > 0
```

## 7. Verification Commands

```bash
# Validate Compose observability profile
docker compose --profile observability config --quiet

# Scrape live Prometheus metrics from gateway replica 1
curl -s http://127.0.0.1:18081/metrics | head -n 30

# Run full observability unit + Redis integration tests
INTEGRATION_TESTS=1 go test -race -v ./internal/shared/logging/... ./internal/shared/telemetry/... ./internal/gateway/metrics/... ./tests/integration/... -run 'TestGatewayInstanceHeartbeat|TestAggregateMetrics|TestTimeSeries|TestTraceCorrelation'
```
