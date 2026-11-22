#!/usr/bin/env bash
set -euo pipefail

# Reproducible local PostgreSQL schema reset for development/testing only.
if [[ "${GATEWAYFORGE_ENV:-development}" == "production" ]]; then
  echo "REFUSED: reset-db.sh must never run in production." >&2
  exit 1
fi

DB_NAME="${POSTGRES_DB:-gatewayforge}"
DB_USER="${POSTGRES_USER:-gatewayforge}"

docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U "$DB_USER" -d "$DB_NAME" <<'SQL'
DROP SCHEMA IF EXISTS public CASCADE;
CREATE SCHEMA public;
GRANT ALL ON SCHEMA public TO CURRENT_USER;
SQL

echo "PostgreSQL schema reset complete for database '$DB_NAME'."
