#!/usr/bin/env bash
set -euo pipefail

required_dirs=(
  "apps/web"
  "cmd/control-plane"
  "cmd/gateway"
  "internal/control"
  "internal/gateway"
  "internal/shared"
  "migrations"
  "api"
  "deploy"
  "tests/integration"
  "tests/e2e"
  "tests/load"
  "docs/adr"
  "docs/security"
  "docs/benchmarks"
  "docs/phases"
  ".github/workflows"
)

missing=0
for dir in "${required_dirs[@]}"; do
  if [[ ! -d "$dir" ]]; then
    echo "MISSING required directory: $dir" >&2
    missing=1
  fi
done

if [[ "$missing" -ne 0 ]]; then
  exit 1
fi

echo "Layout check passed: ${#required_dirs[@]} required directories present."
