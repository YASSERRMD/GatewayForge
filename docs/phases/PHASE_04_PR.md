## Scope

Implements the complete Go reverse-proxy gateway data plane: versioned snapshot schema (`internal/shared/snapshot`), strict deterministic route matchers with segment-boundary safety, lock-free atomic immutable routing table swaps, safe upstream URL resolution & SSRF/DNS-rebinding defenses, streaming `httputil.ReverseProxy` (`FlushInterval: -1`), hop-by-hop & trusted-proxy `X-Forwarded-*` header sanitization, correlation ID & W3C `traceparent` propagation, standard JSON gateway errors, request body/header/deadline limits, smooth weighted round-robin target selection, tuned SSRF-safe HTTP transport with connection pooling & close-on-shutdown, and live Compose proxy smoke verification.

## Task Commits (11 Atomic Commits)

1. `phase 04: implement strict route matchers` (`88d3996`)
2. `phase 04: implement immutable routing table` (`a22868d`)
3. `phase 04: implement safe upstream URL resolution` (`b652d4b`)
4. `phase 04: implement reverse proxy streaming` (`45ed287`)
5. `phase 04: sanitize forwarding headers` (`04f4573`)
6. `phase 04: propagate request IDs and trace context` (`b41b7d7`)
7. `phase 04: add standard gateway errors` (`8da068c`)
8. `phase 04: enforce request resource limits` (`3bec1e8`)
9. `phase 04: add weighted target selection` (`1ddac68`)
10. `phase 04: implement transport and connection management` (`fc3ad2f`)
11. `phase 04: prove no-config and proxy smoke behavior` (PR tip)

## Verification

- `make verify`: PASS (`go fmt`, `go vet`, `golangci-lint` 0 issues, `go test -race ./...`, web lint/typecheck/test/build, `docker compose config`)
- `make integration`: PASS (all PostgreSQL, Redis, auth/RBAC, and live Compose reverse-proxy smoke tests green)
- Saved HTTP smoke transcript in `docs/phases/PHASE_04_TRANSCRIPT.md`.
- Not yet applicable in Phase 04: `make e2e`, `make security-test`, `make load`.
