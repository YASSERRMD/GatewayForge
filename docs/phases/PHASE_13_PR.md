# Phase 13 — Release Workflow, Immutable History and Rollback UI

## Summary
Delivers the full release governance lifecycle in GatewayForge Studio (`/releases`, `/insights`, `/settings`) with semantic diffing, optimistic concurrency locks, audited rollbacks, multi-replica convergence visibility, failed-rollout LKG protection, RBAC boundaries, and 409 conflict resolution UX.

## Key Changes
- **Releases Studio (`/releases`)**: Draft validation & explicit confirmed publication modal, semantic structural config diff (`GET /orgs/{orgId}/draft/diff` with zero secret/hash exposure), paginated immutable release history, optimistic activation/rollback dialogs (`expected_activation_revision`), and live Gateway Fleet Convergence status panel (`healthy`/`stale`/`offline` & version/checksum lag).
- **Audited Rollbacks & LKG Protection**: Explicit `publication.rollback` / `publication.activate` redacted audit records, verified LKG preservation on invalid drafts (422) and tampered gateway snapshot checksums.
- **Insights & Audit Explorer (`/insights`) & Settings (`/settings`)**: Server-side filtering by actor/action/target, defense-in-depth client redaction, safe JSON export, and organization member RBAC view.
- **409 Conflict Resolution UX**: Reusable `ConflictResolutionBanner` enabling operators to reload the latest server revision without blindly overwriting peer edits.
- **Full Lifecycle E2E (`AT-04`, `AT-15`, `AT-16`)**: `apps/web/e2e/release-lifecycle.spec.ts` proving draft -> validate -> publish v1 -> 2-replica live HTTP 200 -> break & publish v2 -> 2-replica 404 -> UI rollback -> 2-replica HTTP 200 convergence -> Audit Explorer verification.

## Verification
- `make verify`
- Live PostgreSQL `-race` integration tests: `TestConcurrentReleaseActivationOptimisticLock`, `TestReleaseRollbackAfterRouteFailureRestoresSnapshotAndAudits`, `TestFailedRolloutPreservesLastKnownGoodAndSurfacesErrors`, `TestPublishAndRollbackAuthorizationBoundaries`
- `cd apps/web && npx playwright test --workers=1` (4/4 passing against live Docker Compose)
- See `docs/phases/PHASE_13_REPORT.md` for full atomic commit ledger.
