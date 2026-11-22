package integration

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestSchemaConsumersAndNoPlaintextKeys(t *testing.T) {
	pool := openIntegrationDB(t)
	resetAndMigrateDB(t, pool)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Verify api_keys contains NO plaintext secret column.
	rows, err := pool.Query(ctx,
		`SELECT column_name FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'api_keys'`,
	)
	if err != nil {
		t.Fatalf("query information_schema: %v", err)
	}
	defer rows.Close()

	hasHash := false
	for rows.Next() {
		var col string
		if err := rows.Scan(&col); err != nil {
			t.Fatalf("scan column_name: %v", err)
		}
		lower := strings.ToLower(col)
		if strings.Contains(lower, "plain") || strings.Contains(lower, "secret") || lower == "key" || lower == "token" || lower == "api_key" {
			t.Fatalf("forbidden plaintext credential column found in api_keys: %q", col)
		}
		if col == "key_hash" {
			hasHash = true
		}
	}
	if !hasHash {
		t.Fatal("expected key_hash column in api_keys")
	}

	var orgA, orgB, consumerA string
	if err := pool.QueryRow(ctx, `INSERT INTO organizations (slug, display_name) VALUES ('ck-a', 'CK A') RETURNING id`).Scan(&orgA); err != nil {
		t.Fatalf("orgA: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO organizations (slug, display_name) VALUES ('ck-b', 'CK B') RETURNING id`).Scan(&orgB); err != nil {
		t.Fatalf("orgB: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO consumers (org_id, name) VALUES ($1, 'mobile-app') RETURNING id`, orgA).Scan(&consumerA); err != nil {
		t.Fatalf("consumerA: %v", err)
	}

	fakeHash := strings.Repeat("a", 64)
	if _, err := pool.Exec(ctx,
		`INSERT INTO api_keys (org_id, consumer_id, key_prefix, key_hash, scopes) VALUES ($1, $2, 'gwf_12345678', $3, ARRAY['orders:read'])`,
		orgA, consumerA, fakeHash,
	); err != nil {
		t.Fatalf("insert hashed api_key: %v", err)
	}

	// Cross-tenant key attachment MUST fail.
	if _, err := pool.Exec(ctx,
		`INSERT INTO api_keys (org_id, consumer_id, key_prefix, key_hash) VALUES ($1, $2, 'gwf_87654321', $3)`,
		orgB, consumerA, strings.Repeat("b", 64),
	); err == nil {
		t.Fatal("expected composite FK failure for cross-tenant api_key")
	}
}
