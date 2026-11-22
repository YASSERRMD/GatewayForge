package migrate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const migrationLockID int64 = 7248190421

// Migration represents a single ordered SQL migration file.
type Migration struct {
	Version  int
	Name     string
	SQL      string
	Checksum string
}

// AppliedRecord describes a migration already recorded in schema_migrations.
type AppliedRecord struct {
	Version   int
	Name      string
	Checksum  string
	AppliedAt time.Time
}

// Load reads and validates all .sql migrations from fsys sorted by numeric version.
func Load(fsys fs.FS) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("read migrations directory: %w", err)
	}

	var list []Migration
	seen := make(map[int]string)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		prefix, _, ok := strings.Cut(entry.Name(), "_")
		if !ok {
			return nil, fmt.Errorf("migration %q must follow NNNN_name.sql format", entry.Name())
		}
		ver, err := strconv.Atoi(prefix)
		if err != nil || ver < 0 {
			return nil, fmt.Errorf("migration %q has invalid numeric prefix %q", entry.Name(), prefix)
		}
		if prev, dup := seen[ver]; dup {
			return nil, fmt.Errorf("duplicate migration version %d (%s and %s)", ver, prev, entry.Name())
		}
		seen[ver] = entry.Name()

		raw, err := fs.ReadFile(fsys, entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}
		sum := sha256.Sum256(raw)
		list = append(list, Migration{
			Version:  ver,
			Name:     entry.Name(),
			SQL:      string(raw),
			Checksum: hex.EncodeToString(sum[:]),
		})
	}

	sort.Slice(list, func(i, j int) bool {
		return list[i].Version < list[j].Version
	})
	return list, nil
}

// Up applies all pending migrations in order and verifies checksums of already-applied ones.
// Returns the count of newly applied migrations.
func Up(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS) (int, error) {
	migrations, err := Load(fsys)
	if err != nil {
		return 0, err
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return 0, fmt.Errorf("acquire migration connection: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", migrationLockID); err != nil {
		return 0, fmt.Errorf("acquire migration advisory lock: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", migrationLockID)
	}()

	const createTableSQL = `
CREATE TABLE IF NOT EXISTS schema_migrations (
	version         INTEGER PRIMARY KEY,
	name            TEXT NOT NULL,
	checksum_sha256 TEXT NOT NULL,
	applied_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);`
	if _, err := conn.Exec(ctx, createTableSQL); err != nil {
		return 0, fmt.Errorf("ensure schema_migrations table: %w", err)
	}

	applied := make(map[int]AppliedRecord)
	rows, err := conn.Query(ctx, "SELECT version, name, checksum_sha256, applied_at FROM schema_migrations ORDER BY version")
	if err != nil {
		return 0, fmt.Errorf("query schema_migrations: %w", err)
	}
	for rows.Next() {
		var rec AppliedRecord
		if err := rows.Scan(&rec.Version, &rec.Name, &rec.Checksum, &rec.AppliedAt); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan schema_migrations: %w", err)
		}
		applied[rec.Version] = rec
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate schema_migrations: %w", err)
	}

	newlyApplied := 0
	for _, m := range migrations {
		if existing, ok := applied[m.Version]; ok {
			if existing.Checksum != m.Checksum {
				return newlyApplied, fmt.Errorf(
					"checksum mismatch for applied migration %d (%s): recorded=%s current=%s",
					m.Version, m.Name, existing.Checksum, m.Checksum,
				)
			}
			continue
		}

		tx, err := conn.Begin(ctx)
		if err != nil {
			return newlyApplied, fmt.Errorf("begin migration %d (%s): %w", m.Version, m.Name, err)
		}
		if _, err := tx.Exec(ctx, m.SQL); err != nil {
			_ = tx.Rollback(ctx)
			return newlyApplied, fmt.Errorf("execute migration %d (%s): %w", m.Version, m.Name, err)
		}
		if _, err := tx.Exec(
			ctx,
			"INSERT INTO schema_migrations (version, name, checksum_sha256) VALUES ($1, $2, $3)",
			m.Version, m.Name, m.Checksum,
		); err != nil {
			_ = tx.Rollback(ctx)
			return newlyApplied, fmt.Errorf("record migration %d (%s): %w", m.Version, m.Name, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return newlyApplied, fmt.Errorf("commit migration %d (%s): %w", m.Version, m.Name, err)
		}
		newlyApplied++
	}

	return newlyApplied, nil
}

// Status returns all applied migration records in ascending version order.
func Status(ctx context.Context, pool *pgxpool.Pool) ([]AppliedRecord, error) {
	rows, err := pool.Query(ctx, "SELECT version, name, checksum_sha256, applied_at FROM schema_migrations ORDER BY version")
	if err != nil {
		return nil, fmt.Errorf("query schema_migrations: %w", err)
	}
	defer rows.Close()

	var out []AppliedRecord
	for rows.Next() {
		var rec AppliedRecord
		if err := rows.Scan(&rec.Version, &rec.Name, &rec.Checksum, &rec.AppliedAt); err != nil {
			return nil, fmt.Errorf("scan schema_migrations: %w", err)
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}
