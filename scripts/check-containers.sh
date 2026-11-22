#!/usr/bin/env bash
set -euo pipefail

for df in deploy/Dockerfile.control-plane deploy/Dockerfile.gateway deploy/Dockerfile.mock-upstream deploy/Dockerfile.web; do
  if [ ! -f "$df" ]; then
    echo "Missing Dockerfile: $df" >&2
    exit 1
  fi
  if grep -Eq '^FROM .*:latest' "$df"; then
    echo "Disallowed :latest base tag in $df" >&2
    exit 1
  fi
  if ! grep -Eq '^USER (appuser|nextjs)' "$df"; then
    echo "Missing non-root USER directive in $df" >&2
    exit 1
  fi
  if [ "$(grep -c '^FROM ' "$df")" -lt 2 ]; then
    echo "Expected multi-stage build in $df" >&2
    exit 1
  fi
done

echo "Container hardening checks passed across all 4 Dockerfiles."
