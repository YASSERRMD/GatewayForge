# CI Hardening and Repository Branch Protection Guidance

## 1. Workflow Hardening (`.github/workflows/ci.yml`)
- **Least-Privilege `GITHUB_TOKEN`**: Root and per-job `permissions` are restricted strictly to `contents: read` (zero write scopes on PR builds).
- **Concurrency Cancellation**: Stale workflow runs on rapid branch pushes are automatically canceled per ref.
- **Blocking Security Job (`Security, container and secret checks`)**:
  - `bash scripts/check-containers.sh` — verifies multi-stage builds, pinned base images, and non-root `USER` across all Dockerfiles.
  - `bash scripts/check-secrets.sh` — blocks tracked `.env` files and hardcoded private keys / cloud tokens / PATs.
  - `go test -race -v ./tests/security/...` — executes adversarial RBAC, SSRF/DNS-rebinding, proxy smuggling/spoofing, JWT/API-key abuse, race contention, Redis/control-plane outage, and graceful shutdown/LKG restart tests.
- **Pinned Linters & Runtime Versions**: Go version sourced from `go.mod`, Node pinned to `24`, `golangci-lint` pinned to `v2.12.2`.

## 2. Recommended GitHub Branch Protection Rules (`main`)
Configure the following ruleset on `main` under **Settings → Rules → Rulesets**:
1. **Require a pull request before merging** (disable direct pushes to `main`).
2. **Require status checks to pass before merging** (strict up-to-date branch):
   - `Go lint, vet, race tests and build`
   - `Web lint, typecheck, test and build`
   - `Docker Compose validation`
   - `Security, container and secret checks`
3. **Block force pushes** and **block branch deletions** on `main`.
4. **Preserve atomic commit history**: enable merge commits (`Allow merge commits`), disable squash/rebase overwrites when atomic auditability is required.
5. **Secrets hygiene**: never store production `SESSION_SECRET`, `GATEWAY_SYNC_TOKEN`, or database passwords in repository files or PR comments; inject via GitHub Environments / secret managers only.
