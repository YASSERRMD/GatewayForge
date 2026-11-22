## Scope

Implements the versioned SQL migration engine with advisory locking and SHA-256 checksum verification, full multi-tenant PostgreSQL relational schema (`0000`–`0009`), RBAC permission matrix (DB + Go + ADR-0002), DB-enforced immutability triggers for publications and audit events, typed `pgx` store with transactions and optimistic concurrency, and deterministic demo fixture seeder.

## Task Commits (11 Atomic Commits)

1. `phase 02: introduce SQL migration engine` (`7071727`)
2. `phase 02: model organizations and users` (`94e7791`)
3. `phase 02: model roles and permissions` (`8fa16f5`)
4. `phase 02: model services and environments` (`13eb6ac`)
5. `phase 02: model upstream targets and pools` (`b92e264`)
6. `phase 02: model route definitions` (`f525253`)
7. `phase 02: model consumers and credential metadata` (`7341a64`)
8. `phase 02: model typed policy attachments` (`0278e57`)
9. `phase 02: model immutable publications` (`3739ebf`)
10. `phase 02: model append-only audit events` (`7d25069`)
11. `phase 02: implement typed store and fixtures` (PR tip)

## Verification

- `make verify`: PASS (`go fmt`, `go vet`, `golangci-lint` 0 issues, `go test -race ./...`, web lint/typecheck/test/build, `docker compose config`)
- `make integration`: PASS (all PostgreSQL schema constraint, composite FK isolation, trigger immutability, migration checksum, and store integration tests green against live PostgreSQL 16)
- `make db-reset && make migrate && make seed`: PASS
- Not yet applicable in Phase 02: `make e2e`, `make security-test`, `make load`.

## Security & Schema Notes

- Composite FKs `(org_id, id)` prevent cross-tenant entity linking at the database constraint layer.
- `publications` and `audit_events` enforce immutability via PostgreSQL `BEFORE UPDATE OR DELETE` triggers.
- `api_keys` contains no plaintext credential column (`key_hash` only).
