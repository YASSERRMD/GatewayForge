package integration

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestSchemaImmutablePublicationsAndOptimisticVersion(t *testing.T) {
	pool := openIntegrationDB(t)
	resetAndMigrateDB(t, pool)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var orgA, orgB string
	if err := pool.QueryRow(ctx, `INSERT INTO organizations (slug, display_name) VALUES ('pub-a', 'Pub A') RETURNING id`).Scan(&orgA); err != nil {
		t.Fatalf("orgA: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO organizations (slug, display_name) VALUES ('pub-b', 'Pub B') RETURNING id`).Scan(&orgB); err != nil {
		t.Fatalf("orgB: %v", err)
	}

	checksum := strings.Repeat("a", 64)
	var pubID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO publications (org_id, version, draft_revision, snapshot_json, checksum_sha256, notes)
		 VALUES ($1, 1, 1, '{"version":1,"routes":[]}', $2, 'initial') RETURNING id`,
		orgA, checksum,
	).Scan(&pubID); err != nil {
		t.Fatalf("insert publication v1: %v", err)
	}

	// Optimistic version conflict: duplicate (org_id, version=1) MUST fail.
	if _, err := pool.Exec(ctx,
		`INSERT INTO publications (org_id, version, draft_revision, snapshot_json, checksum_sha256)
		 VALUES ($1, 1, 2, '{"version":1,"routes":[]}', $2)`,
		orgA, checksum,
	); err == nil {
		t.Fatal("expected unique violation on duplicate (org_id, version)")
	}

	// Immutability trigger: UPDATE MUST fail.
	if _, err := pool.Exec(ctx, `UPDATE publications SET notes = 'tampered' WHERE id = $1`, pubID); err == nil {
		t.Fatal("expected trigger error when updating published snapshot")
	}

	// Immutability trigger: DELETE MUST fail.
	if _, err := pool.Exec(ctx, `DELETE FROM publications WHERE id = $1`, pubID); err == nil {
		t.Fatal("expected trigger error when deleting published snapshot")
	}

	// Activate publication for orgA succeeds; cross-tenant activation for orgB fails.
	if _, err := pool.Exec(ctx,
		`INSERT INTO active_publications (org_id, publication_id) VALUES ($1, $2)`,
		orgA, pubID,
	); err != nil {
		t.Fatalf("activate publication for orgA: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO active_publications (org_id, publication_id) VALUES ($1, $2)`,
		orgB, pubID,
	); err == nil {
		t.Fatal("expected FK failure activating orgA publication in orgB")
	}
}
