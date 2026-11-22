package integration

import (
	"context"
	"testing"
	"time"
)

func TestSchemaUpstreamPoolsAndTargets(t *testing.T) {
	pool := openIntegrationDB(t)
	resetAndMigrateDB(t, pool)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var orgA, orgB, envA string
	if err := pool.QueryRow(ctx, `INSERT INTO organizations (slug, display_name) VALUES ('up-a', 'Up A') RETURNING id`).Scan(&orgA); err != nil {
		t.Fatalf("insert orgA: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO organizations (slug, display_name) VALUES ('up-b', 'Up B') RETURNING id`).Scan(&orgB); err != nil {
		t.Fatalf("insert orgB: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO environments (org_id, slug, display_name) VALUES ($1, 'dev', 'Dev') RETURNING id`, orgA).Scan(&envA); err != nil {
		t.Fatalf("insert envA: %v", err)
	}

	// Out-of-bounds connect timeout MUST fail.
	if _, err := pool.Exec(ctx,
		`INSERT INTO upstream_pools (org_id, env_id, name, connect_timeout_ms) VALUES ($1, $2, 'bad-timeout', 50)`,
		orgA, envA,
	); err == nil {
		t.Fatal("expected check constraint failure for connect_timeout_ms=50")
	}

	var poolID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO upstream_pools (org_id, env_id, name, destination_class) VALUES ($1, $2, 'echo-pool', 'mock_local') RETURNING id`,
		orgA, envA,
	).Scan(&poolID); err != nil {
		t.Fatalf("insert upstream pool: %v", err)
	}

	// Valid target succeeds.
	if _, err := pool.Exec(ctx,
		`INSERT INTO upstream_targets (org_id, pool_id, url, weight) VALUES ($1, $2, 'http://mock-upstream:8089', 100)`,
		orgA, poolID,
	); err != nil {
		t.Fatalf("insert valid target: %v", err)
	}

	// Invalid weight (0 or >1000) MUST fail.
	if _, err := pool.Exec(ctx,
		`INSERT INTO upstream_targets (org_id, pool_id, url, weight) VALUES ($1, $2, 'http://mock-upstream:8090', 0)`,
		orgA, poolID,
	); err == nil {
		t.Fatal("expected check violation for weight=0")
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO upstream_targets (org_id, pool_id, url, weight) VALUES ($1, $2, 'http://mock-upstream:8090', 1001)`,
		orgA, poolID,
	); err == nil {
		t.Fatal("expected check violation for weight=1001")
	}

	// Non-HTTP URL MUST fail.
	if _, err := pool.Exec(ctx,
		`INSERT INTO upstream_targets (org_id, pool_id, url, weight) VALUES ($1, $2, 'file:///etc/passwd', 10)`,
		orgA, poolID,
	); err == nil {
		t.Fatal("expected check violation for file:// URL")
	}

	// Cross-tenant target attaching to orgA's pool MUST fail.
	if _, err := pool.Exec(ctx,
		`INSERT INTO upstream_targets (org_id, pool_id, url, weight) VALUES ($1, $2, 'http://mock-upstream:8091', 10)`,
		orgB, poolID,
	); err == nil {
		t.Fatal("expected composite FK violation for cross-tenant target")
	}
}
