## Scope

Delivers the authenticated Go Chi control-plane API with RFC 9457 Problem Details, race-safe one-time dev first-admin bootstrap, Argon2id password storage, PostgreSQL-backed revocable sessions with CSRF protection (ADR-0003), explicit multi-organization context, fail-deny RBAC middleware, organization CRUD, membership administration with last-owner protection, strict JSON validation / optimistic concurrency / idempotency, redacted audit recording & query API, and OpenAPI 3.1 contract verified against the live router.

## Task Commits (11 Atomic Commits)

1. `phase 03: implement control-plane API routing` (`e395490`)
2. `phase 03: add first-admin bootstrap flow` (`97cd56e`)
3. `phase 03: implement password credential storage` (`cd2faad`)
4. `phase 03: implement login and logout` (`832cd58`)
5. `phase 03: implement current-user and org context` (`950b772`)
6. `phase 03: enforce RBAC middleware` (`4c67648`)
7. `phase 03: implement organization CRUD` (`2ff2e66`)
8. `phase 03: implement membership administration` (`f1ff665`)
9. `phase 03: add validation and optimistic concurrency` (`201aabb`)
10. `phase 03: implement audit recording middleware` (`aaa924f`)
11. `phase 03: publish initial OpenAPI contract` (PR tip)

## Verification

- `make verify`: PASS (`go fmt`, `go vet`, `golangci-lint` 0 issues, `go test -race ./...`, web lint/typecheck/test/build, `docker compose config`)
- `make integration`: PASS (bootstrap concurrency race, session/CSRF/logout revocation, org isolation, membership privilege escalation & last-owner safety, optimistic concurrency race, idempotency, secret redaction in audit trail)
- `TestOpenAPIContractMatchesRouter`: PASS (all OpenAPI 3.1 endpoints verified mounted in live Chi router)
- Not yet applicable in Phase 03: `make e2e`, `make security-test`, `make load`.
