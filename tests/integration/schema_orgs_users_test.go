package integration

import (
	"context"
	"testing"
	"time"
)

func TestSchemaOrgsUsersMembershipsConstraints(t *testing.T) {
	pool := openIntegrationDB(t)
	resetAndMigrateDB(t, pool)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var orgID string
	err := pool.QueryRow(ctx,
		`INSERT INTO organizations (slug, display_name) VALUES ('acme-corp', 'Acme Corp') RETURNING id`,
	).Scan(&orgID)
	if err != nil {
		t.Fatalf("insert organization: %v", err)
	}

	// Duplicate org slug must fail.
	if _, err := pool.Exec(ctx,
		`INSERT INTO organizations (slug, display_name) VALUES ('acme-corp', 'Acme Duplicate')`,
	); err == nil {
		t.Fatal("expected unique violation on duplicate org slug")
	}

	// Invalid org slug must fail check constraint.
	if _, err := pool.Exec(ctx,
		`INSERT INTO organizations (slug, display_name) VALUES ('BAD SLUG!', 'Bad')`,
	); err == nil {
		t.Fatal("expected check violation on invalid org slug")
	}

	var userID string
	err = pool.QueryRow(ctx,
		`INSERT INTO users (email, display_name, password_hash) VALUES ('admin@acme.test', 'Admin User', 'argon2id$placeholder') RETURNING id`,
	).Scan(&userID)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	// Uppercase email must fail check constraint.
	if _, err := pool.Exec(ctx,
		`INSERT INTO users (email, display_name, password_hash) VALUES ('ADMIN2@acme.test', 'Upper', 'hash')`,
	); err == nil {
		t.Fatal("expected lowercase email check violation")
	}

	// Membership creation and duplicate prevention.
	if _, err := pool.Exec(ctx,
		`INSERT INTO memberships (org_id, user_id, role) VALUES ($1, $2, 'owner')`,
		orgID, userID,
	); err != nil {
		t.Fatalf("insert membership: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO memberships (org_id, user_id, role) VALUES ($1, $2, 'admin')`,
		orgID, userID,
	); err == nil {
		t.Fatal("expected unique violation on duplicate org/user membership")
	}
}
