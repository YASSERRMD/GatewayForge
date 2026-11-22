package integration

import (
	"context"
	"testing"
	"time"
)

func TestSchemaRoutesConstraintsAndIsolation(t *testing.T) {
	pool := openIntegrationDB(t)
	resetAndMigrateDB(t, pool)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var orgA, orgB, envA, envB, svcA, svcB, poolA, poolB string
	if err := pool.QueryRow(ctx, `INSERT INTO organizations (slug, display_name) VALUES ('rt-a', 'RT A') RETURNING id`).Scan(&orgA); err != nil {
		t.Fatalf("orgA: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO organizations (slug, display_name) VALUES ('rt-b', 'RT B') RETURNING id`).Scan(&orgB); err != nil {
		t.Fatalf("orgB: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO environments (org_id, slug, display_name) VALUES ($1, 'dev', 'Dev') RETURNING id`, orgA).Scan(&envA); err != nil {
		t.Fatalf("envA: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO environments (org_id, slug, display_name) VALUES ($1, 'dev', 'Dev') RETURNING id`, orgB).Scan(&envB); err != nil {
		t.Fatalf("envB: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO services (org_id, env_id, slug, name) VALUES ($1, $2, 'echo', 'Echo') RETURNING id`, orgA, envA).Scan(&svcA); err != nil {
		t.Fatalf("svcA: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO services (org_id, env_id, slug, name) VALUES ($1, $2, 'echo', 'Echo') RETURNING id`, orgB, envB).Scan(&svcB); err != nil {
		t.Fatalf("svcB: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO upstream_pools (org_id, env_id, name) VALUES ($1, $2, 'p1') RETURNING id`, orgA, envA).Scan(&poolA); err != nil {
		t.Fatalf("poolA: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO upstream_pools (org_id, env_id, name) VALUES ($1, $2, 'p1') RETURNING id`, orgB, envB).Scan(&poolB); err != nil {
		t.Fatalf("poolB: %v", err)
	}

	// Cross-tenant upstream pool reference MUST fail.
	if _, err := pool.Exec(ctx,
		`INSERT INTO routes (org_id, env_id, service_id, upstream_pool_id, name, host, path_prefix, methods)
		 VALUES ($1, $2, $3, $4, 'cross-pool', 'api.local', '/echo', ARRAY['GET'])`,
		orgA, envA, svcA, poolB,
	); err == nil {
		t.Fatal("expected FK failure for cross-tenant upstream_pool_id")
	}

	// Cross-tenant service reference MUST fail.
	if _, err := pool.Exec(ctx,
		`INSERT INTO routes (org_id, env_id, service_id, upstream_pool_id, name, host, path_prefix, methods)
		 VALUES ($1, $2, $3, $4, 'cross-svc', 'api.local', '/echo', ARRAY['GET'])`,
		orgA, envA, svcB, poolA,
	); err == nil {
		t.Fatal("expected FK failure for cross-tenant service_id")
	}

	// Valid route succeeds.
	if _, err := pool.Exec(ctx,
		`INSERT INTO routes (org_id, env_id, service_id, upstream_pool_id, name, host, path_prefix, methods, priority)
		 VALUES ($1, $2, $3, $4, 'echo-get', 'api.local', '/echo', ARRAY['GET'], 100)`,
		orgA, envA, svcA, poolA,
	); err != nil {
		t.Fatalf("insert valid route: %v", err)
	}

	// Identical matcher + priority MUST fail conflict uniqueness.
	if _, err := pool.Exec(ctx,
		`INSERT INTO routes (org_id, env_id, service_id, upstream_pool_id, name, host, path_prefix, methods, priority)
		 VALUES ($1, $2, $3, $4, 'echo-get-dup', 'api.local', '/echo', ARRAY['GET'], 100)`,
		orgA, envA, svcA, poolA,
	); err == nil {
		t.Fatal("expected unique violation on conflicting route matcher")
	}

	// Invalid HTTP method MUST fail check constraint.
	if _, err := pool.Exec(ctx,
		`INSERT INTO routes (org_id, env_id, service_id, upstream_pool_id, name, host, path_prefix, methods)
		 VALUES ($1, $2, $3, $4, 'bad-method', 'api.local', '/bad', ARRAY['TRACE'])`,
		orgA, envA, svcA, poolA,
	); err == nil {
		t.Fatal("expected check constraint failure for unsupported method TRACE")
	}
}
