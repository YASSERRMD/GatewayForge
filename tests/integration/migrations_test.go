package integration

import (
	"context"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/YASSERRMD/GatewayForge/internal/control/migrate"
	"github.com/YASSERRMD/GatewayForge/internal/shared/db"
	"github.com/YASSERRMD/GatewayForge/migrations"
)

func openIntegrationDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if os.Getenv("INTEGRATION_TESTS") != "1" {
		t.Skip("set INTEGRATION_TESTS=1 to run PostgreSQL integration tests")
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://gatewayforge:gatewayforge_local_dev_pw@127.0.0.1:5439/gatewayforge?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect integration postgres: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func resetAndMigrateDB(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := pool.Exec(ctx, "DROP SCHEMA IF EXISTS public CASCADE; CREATE SCHEMA public;"); err != nil {
		t.Fatalf("reset public schema: %v", err)
	}
	if _, err := migrate.Up(ctx, pool, migrations.Files); err != nil {
		t.Fatalf("apply embedded migrations: %v", err)
	}
}

func TestMigrationEngineFreshAndChecksumPolicy(t *testing.T) {
	pool := openIntegrationDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if _, err := pool.Exec(ctx, "DROP SCHEMA IF EXISTS public CASCADE; CREATE SCHEMA public;"); err != nil {
		t.Fatalf("reset public schema: %v", err)
	}

	applied, err := migrate.Up(ctx, pool, migrations.Files)
	if err != nil {
		t.Fatalf("initial migrate.Up failed: %v", err)
	}
	if applied < 1 {
		t.Fatalf("expected at least 1 migration applied, got %d", applied)
	}

	// Second run must be idempotent.
	appliedAgain, err := migrate.Up(ctx, pool, migrations.Files)
	if err != nil {
		t.Fatalf("idempotent migrate.Up failed: %v", err)
	}
	if appliedAgain != 0 {
		t.Fatalf("expected 0 migrations on second run, got %d", appliedAgain)
	}

	// Tampering with an already-applied migration checksum must fail loudly.
	tampered := fstest.MapFS{
		"0000_extensions.sql": &fstest.MapFile{
			Data: []byte("-- modified SQL\nCREATE EXTENSION IF NOT EXISTS pgcrypto;\n"),
		},
	}
	_, err = migrate.Up(ctx, pool, tampered)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected checksum mismatch error, got: %v", err)
	}

	// Verify helper resetAndMigrateDB and Status() return recorded checksums.
	resetAndMigrateDB(t, pool)
	records, err := migrate.Status(ctx, pool)
	if err != nil {
		t.Fatalf("migrate.Status failed: %v", err)
	}
	if len(records) < 1 || len(records[0].Checksum) != 64 {
		t.Fatalf("unexpected migration status records: %+v", records)
	}
}
