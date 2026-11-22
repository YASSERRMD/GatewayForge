package integration

import (
	"context"
	"testing"
	"time"
)

func TestSchemaServicesAndEnvironments(t *testing.T) {
	pool := openIntegrationDB(t)
	resetAndMigrateDB(t, pool)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var orgA, orgB string
	if err := pool.QueryRow(ctx, `INSERT INTO organizations (slug, display_name) VALUES ('org-alpha', 'Alpha') RETURNING id`).Scan(&orgA); err != nil {
		t.Fatalf("insert orgA: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO organizations (slug, display_name) VALUES ('org-beta', 'Beta') RETURNING id`).Scan(&orgB); err != nil {
		t.Fatalf("insert orgB: %v", err)
	}

	var envA, envB string
	if err := pool.QueryRow(ctx, `INSERT INTO environments (org_id, slug, display_name) VALUES ($1, 'dev', 'Development') RETURNING id`, orgA).Scan(&envA); err != nil {
		t.Fatalf("insert envA: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO environments (org_id, slug, display_name) VALUES ($1, 'dev', 'Development') RETURNING id`, orgB).Scan(&envB); err != nil {
		t.Fatalf("insert envB: %v", err)
	}

	// Cross-tenant FK (orgA service referencing orgB environment) MUST fail.
	if _, err := pool.Exec(ctx,
		`INSERT INTO services (org_id, env_id, slug, name) VALUES ($1, $2, 'orders', 'Orders')`,
		orgA, envB,
	); err == nil {
		t.Fatal("expected composite FK violation for cross-tenant environment reference")
	}

	var svcID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO services (org_id, env_id, slug, name) VALUES ($1, $2, 'orders', 'Orders API') RETURNING id`,
		orgA, envA,
	).Scan(&svcID); err != nil {
		t.Fatalf("insert service: %v", err)
	}

	// Duplicate active slug in same org/env MUST fail.
	if _, err := pool.Exec(ctx,
		`INSERT INTO services (org_id, env_id, slug, name) VALUES ($1, $2, 'orders', 'Duplicate Orders')`,
		orgA, envA,
	); err == nil {
		t.Fatal("expected unique violation on duplicate active service slug")
	}

	// Tombstone (soft delete) allows recreating same slug safely.
	if _, err := pool.Exec(ctx, `UPDATE services SET deleted_at = NOW() WHERE id = $1`, svcID); err != nil {
		t.Fatalf("tombstone service: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO services (org_id, env_id, slug, name) VALUES ($1, $2, 'orders', 'Orders API v2')`,
		orgA, envA,
	); err != nil {
		t.Fatalf("expected recreation after tombstone to succeed, got: %v", err)
	}
}
