package integration

import (
	"context"
	"testing"
	"time"
)

func TestSchemaAuditEventsAppendOnlyAndPagination(t *testing.T) {
	pool := openIntegrationDB(t)
	resetAndMigrateDB(t, pool)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var orgID string
	if err := pool.QueryRow(ctx, `INSERT INTO organizations (slug, display_name) VALUES ('aud-org', 'Audit Org') RETURNING id`).Scan(&orgID); err != nil {
		t.Fatalf("insert org: %v", err)
	}

	var firstID, secondID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO audit_events (org_id, actor_email, action, target_type, target_id, request_id, after_redacted)
		 VALUES ($1, 'admin@aud.test', 'service.create', 'service', 'svc-1', 'req-001', '{"name":"Orders"}') RETURNING id`,
		orgID,
	).Scan(&firstID); err != nil {
		t.Fatalf("insert first audit event: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO audit_events (org_id, actor_email, action, target_type, target_id, request_id, after_redacted)
		 VALUES ($1, 'admin@aud.test', 'route.create', 'route', 'rt-1', 'req-002', '{"path":"/orders"}') RETURNING id`,
		orgID,
	).Scan(&secondID); err != nil {
		t.Fatalf("insert second audit event: %v", err)
	}

	// UPDATE MUST fail.
	if _, err := pool.Exec(ctx, `UPDATE audit_events SET action = 'tampered' WHERE id = $1`, firstID); err == nil {
		t.Fatal("expected trigger error on UPDATE of audit_events")
	}

	// DELETE MUST fail.
	if _, err := pool.Exec(ctx, `DELETE FROM audit_events WHERE id = $1`, firstID); err == nil {
		t.Fatal("expected trigger error on DELETE of audit_events")
	}

	// Verify deterministic newest-first pagination.
	rows, err := pool.Query(ctx,
		`SELECT id, action FROM audit_events WHERE org_id = $1 ORDER BY created_at DESC, id DESC LIMIT 10`,
		orgID,
	)
	if err != nil {
		t.Fatalf("query audit_events: %v", err)
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		var action string
		if err := rows.Scan(&id, &action); err != nil {
			t.Fatalf("scan audit row: %v", err)
		}
		ids = append(ids, id)
	}
	if len(ids) != 2 || ids[0] != secondID || ids[1] != firstID {
		t.Fatalf("expected newest-first order [%d, %d], got %v", secondID, firstID, ids)
	}
}
