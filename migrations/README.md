# GatewayForge SQL Migrations

## Policy

1. **Forward-only versioned migrations**: Each file uses a four-digit monotonically increasing prefix (`NNNN_description.sql`) and executes inside a single PostgreSQL transaction.
2. **Checksum immutability**: The migration engine computes SHA-256 checksums for every migration and records them in `schema_migrations`. Modifying an already-applied migration fails loudly at startup.
3. **Concurrency safety**: The engine acquires a PostgreSQL session advisory lock before checking or applying migrations so concurrent control-plane replicas never race.
4. **Rollback guidance**: Never run destructive `DROP` statements automatically in production. To roll back a schema change, restore from a point-in-time backup or ship a new forward compensating migration (`NNNN+1_revert_...sql`) that preserves auditability and data integrity. In local development, run `make db-reset` followed by `make migrate`.
