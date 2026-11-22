# Phase 05 Report — Management CRUD and Dynamic Configuration

- **Branch:** `phase/05-dynamic-config`
- **PR:** Pending PR creation
- **Merge commit:** Pending merge
- **Date:** 2026-11-22

## Completed Tasks (01–11)

1. `220ef7e5f4b17edcc4282b208136877752bb67b6` — `phase 05: implement service API`
2. `4f46c244c993d921bc1e612f5d54b18e5a5bae28` — `phase 05: implement upstream API`
3. `1346cfd6dc146a40f3b6c7c12176b675a2b945e6` — `phase 05: implement route API`
4. `ce8ac502122a9af16cf2f0310d59b60dbe171a53` — `phase 05: implement draft configuration compiler`
5. `b92f8233ca82cfb09dcd91046626d332313fd456` — `phase 05: implement configuration validation endpoint`
6. `13d1b9a53d76b3359fb3b0e6321bab7b9756a50c` — `phase 05: implement publication transaction`
7. `3ee39c31b780aaaa51635722dbe7a51575db34e0` — `phase 05: implement authenticated snapshot retrieval`
8. `39d465b42ba30b68924b87d26d2acde2431ab0cc` — `phase 05: implement Redis config-change events`
9. `e657757ae9e7728b93abea078916533581f97115` — `phase 05: implement gateway snapshot polling`
10. `0f085ac3bbded8312d4186cd870992da2ac8819d` — `phase 05: implement last-known-good fallback`
11. *(HEAD commit of this PR)* — `phase 05: verify two-gateway convergence`

## Verification Evidence

- `make verify`: PASS (`go fmt`, `go vet`, `golangci-lint` 0 issues, ESLint, `tsc --noEmit`, `go test -race ./...`, Vitest, Go/Web builds, `docker compose config --quiet`).
- `make integration`: PASS including `TestTwoGatewayReplicaZeroRestartConvergence` proving:
  - Service, mock upstream, and route v1 published via control plane -> BOTH gateway replicas converge to identical v1 checksum and proxy live HTTP traffic to Compose `mock-upstream`.
  - Route v2 published without restarting gateways -> BOTH replicas converge to identical v2 checksum and serve `201 Created` on `/v2/status/201`.
  - Rollback to v1 -> BOTH replicas converge back and `/v2/status/201` returns 404.
  - Periodic polling reconciles updates even when Redis pub/sub is unavailable.
  - Poisoned/tampered snapshots are rejected while last-known-good remains active; restart restores verified LKG from disk during control-plane outage.

## Architecture & Security Notes

- Control/data trust boundary strictly preserved: gateways pull read-only checksummed snapshots via constant-time Bearer `GATEWAY_SYNC_TOKEN` (rotation-ready comma-separated tokens) and never hold admin write credentials.
- Redis pub/sub carries minimal invalidation hints only; gateways never trust pub/sub payload as configuration.
- Second replica `gateway-2` added to `docker-compose.yml` on `127.0.0.1:18082`.

## Confirmation

- No secrets committed, 11 atomic commits on `phase/05-dynamic-config`, clean working tree.
