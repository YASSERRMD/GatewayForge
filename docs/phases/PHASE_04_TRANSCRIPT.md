# Phase 04 — Verified HTTP Proxy Smoke Transcript

Recorded from live execution of `INTEGRATION_TESTS=1 go test -race -v -run TestGatewayNoConfigAndLiveComposeProxySmoke ./tests/integration/...` against Compose `gatewayforge-mock-upstream-1` (`http://127.0.0.1:18089`).

## 1. No-Config Default Deny (No Open Proxy)

```http
GET /echo/test HTTP/1.1
Host: api.local

HTTP/1.1 404 Not Found
Content-Type: application/json; charset=utf-8
Cache-Control: no-store
X-Request-Id: gw_<generated>

{"status":404,"code":"route_not_found","message":"no configured route matches this request","request_id":"gw_<generated>","trace_id":"<32hex>"}
```

## 2. Configured Route -> Live Compose Mock Upstream (Header Sanitization + Trace Propagation)

```http
POST /echo/orders?env=smoke HTTP/1.1
Host: api.local
X-Request-Id: smoke-phase04-001
X-Forwarded-For: 1.2.3.4
Connection: X-Smuggled-Hop
X-Smuggled-Hop: drop-me

{"item":"widget"}

HTTP/1.1 200 OK
Content-Type: application/json; charset=utf-8
X-Mock-Instance: mock-upstream-1
X-Request-Id: smoke-phase04-001

{
  "instance": "mock-upstream-1",
  "method": "POST",
  "path": "/echo/orders",
  "raw_query": "env=smoke",
  "headers": {
    "Traceparent": ["00-<trace_id>-<span_id>-01"],
    "X-Forwarded-For": ["127.0.0.1"],
    "X-Forwarded-Host": ["api.local"],
    "X-Forwarded-Proto": ["http"],
    "X-Request-Id": ["smoke-phase04-001"]
  },
  "body": "{\"item\":\"widget\"}",
  "body_size": 17
}
```

Verified:
- Spoofed `X-Forwarded-For: 1.2.3.4` replaced with authoritative peer IP `127.0.0.1`.
- `Connection`-nominated `X-Smuggled-Hop` stripped before reaching upstream.
- `X-Request-Id` and W3C `Traceparent` propagated end-to-end.

## 3. Strip-Prefix Status Preservation & Unknown Host Denial

- `GET /v1/mock/status/201` (`Host: api.local`, `strip_prefix=true`) -> proxied to `/status/201` -> `HTTP/1.1 201 Created`.
- `GET /echo/orders` (`Host: evil.example`) -> `HTTP/1.1 404 Not Found` (`code: "route_not_found"`).
