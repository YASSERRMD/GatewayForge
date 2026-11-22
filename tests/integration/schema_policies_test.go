package integration

import (
	"context"
	"testing"
	"time"
)

func TestSchemaPoliciesAndAttachments(t *testing.T) {
	pool := openIntegrationDB(t)
	resetAndMigrateDB(t, pool)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var orgA, orgB, envA, svcA, poolA, routeA string
	if err := pool.QueryRow(ctx, `INSERT INTO organizations (slug, display_name) VALUES ('pol-a', 'Pol A') RETURNING id`).Scan(&orgA); err != nil {
		t.Fatalf("orgA: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO organizations (slug, display_name) VALUES ('pol-b', 'Pol B') RETURNING id`).Scan(&orgB); err != nil {
		t.Fatalf("orgB: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO environments (org_id, slug, display_name) VALUES ($1, 'dev', 'Dev') RETURNING id`, orgA).Scan(&envA); err != nil {
		t.Fatalf("envA: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO services (org_id, env_id, slug, name) VALUES ($1, $2, 'orders', 'Orders') RETURNING id`, orgA, envA).Scan(&svcA); err != nil {
		t.Fatalf("svcA: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO upstream_pools (org_id, env_id, name) VALUES ($1, $2, 'p1') RETURNING id`, orgA, envA).Scan(&poolA); err != nil {
		t.Fatalf("poolA: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO routes (org_id, env_id, service_id, upstream_pool_id, name, host, path_prefix, methods)
		 VALUES ($1, $2, $3, $4, 'r1', 'api.local', '/orders', ARRAY['GET']) RETURNING id`,
		orgA, envA, svcA, poolA,
	).Scan(&routeA); err != nil {
		t.Fatalf("routeA: %v", err)
	}

	// Unknown policy type MUST fail.
	if _, err := pool.Exec(ctx,
		`INSERT INTO policies (org_id, name, policy_type, config) VALUES ($1, 'bad-type', 'lua_script', '{}')`,
		orgA,
	); err == nil {
		t.Fatal("expected check violation for unknown policy_type")
	}

	// Unknown schema version MUST fail.
	if _, err := pool.Exec(ctx,
		`INSERT INTO policies (org_id, name, policy_type, schema_version, config) VALUES ($1, 'bad-ver', 'rate_limit', 99, '{}')`,
		orgA,
	); err == nil {
		t.Fatal("expected check violation for unsupported schema_version=99")
	}

	var polID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO policies (org_id, name, policy_type, config) VALUES ($1, 'std-rl', 'rate_limit', '{"rpm":60}') RETURNING id`,
		orgA,
	).Scan(&polID); err != nil {
		t.Fatalf("insert valid policy: %v", err)
	}

	// Scope mismatch (scope_type='route' without route_id) MUST fail.
	if _, err := pool.Exec(ctx,
		`INSERT INTO policy_attachments (org_id, policy_id, scope_type) VALUES ($1, $2, 'route')`,
		orgA, polID,
	); err == nil {
		t.Fatal("expected check violation when scope_type='route' lacks route_id")
	}

	// Scope mismatch (both service_id and route_id set) MUST fail.
	if _, err := pool.Exec(ctx,
		`INSERT INTO policy_attachments (org_id, policy_id, scope_type, service_id, route_id) VALUES ($1, $2, 'route', $3, $4)`,
		orgA, polID, svcA, routeA,
	); err == nil {
		t.Fatal("expected check violation when both service_id and route_id set")
	}

	// Valid route attachment succeeds.
	if _, err := pool.Exec(ctx,
		`INSERT INTO policy_attachments (org_id, policy_id, scope_type, route_id) VALUES ($1, $2, 'route', $3)`,
		orgA, polID, routeA,
	); err != nil {
		t.Fatalf("valid policy attachment failed: %v", err)
	}

	// Cross-tenant policy attachment MUST fail.
	if _, err := pool.Exec(ctx,
		`INSERT INTO policy_attachments (org_id, policy_id, scope_type) VALUES ($1, $2, 'org')`,
		orgB, polID,
	); err == nil {
		t.Fatal("expected FK failure for cross-tenant policy attachment")
	}
}
