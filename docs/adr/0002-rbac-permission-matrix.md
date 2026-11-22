# ADR-0002: Organization RBAC Permission Matrix

- **Status:** Accepted
- **Date:** 2026-11-22

## Decision

GatewayForge enforces fail-deny organization-scoped RBAC with four canonical roles stored in PostgreSQL (`roles`, `memberships`, `role_permissions`) and evaluated server-side in `internal/control/rbac`:

| Capability | `viewer` | `developer` | `admin` | `owner` |
|---|:---:|:---:|:---:|:---:|
| Read org, services, routes, upstreams, policies, releases, audit, metrics | Yes | Yes | Yes | Yes |
| Edit draft services, upstreams, routes, policies; use request console | No | Yes | Yes | Yes |
| Manage consumers & issue/revoke API keys | No | No | Yes | Yes |
| Publish, activate, and roll back configuration releases | No | No | Yes | Yes |
| Update organization settings & manage non-owner memberships | No | No | Yes | Yes |
| Grant/revoke `owner` role & delete organization | No | No | No | Yes |

## Rationale

Separating `config:write` (draft edits) from `release:publish`/`release:activate`/`release:rollback` prevents a developer role from unilaterally pushing unreviewed traffic changes to live gateway replicas.
