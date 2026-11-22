#!/usr/bin/env bash
set -euo pipefail

# Verify no real .env files (outside .env.example) are tracked in git.
tracked_env="$(git ls-files | grep -E '(^|/)\.env(\..+)?$' | grep -v '\.env\.example$' || true)"
if [ -n "$tracked_env" ]; then
  echo "Tracked secret environment files detected:" >&2
  echo "$tracked_env" >&2
  exit 1
fi

# Scan tracked source/config files for private keys, cloud keys, or GitHub PATs.
# Excludes test fixtures that intentionally construct synthetic strings.
patterns='(-----BEGIN (RSA |EC |OPENSSH )?PRIVATE KEY-----|AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9]{36}|github_pat_[A-Za-z0-9_]{20,})'
matches="$(git ls-files | grep -vE '(_test\.go|\.test\.tsx?|scripts/check-secrets\.sh)$' | xargs grep -En "$patterns" 2>/dev/null || true)"
if [ -n "$matches" ]; then
  echo "Potential hardcoded secrets detected:" >&2
  echo "$matches" >&2
  exit 1
fi

echo "Repository secret scan passed (0 tracked .env files, 0 hardcoded private keys/tokens)."
