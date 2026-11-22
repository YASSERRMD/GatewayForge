package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/YASSERRMD/GatewayForge/internal/control/store"
)

func TestStoreCRUDIsolationAndFixtures(t *testing.T) {
	pool := openIntegrationDB(t)
	resetAndMigrateDB(t, pool)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	st := store.New(pool)

	// 1. Seed deterministic fixtures twice to verify idempotency.
	first, err := st.SeedDemoFixtures(ctx)
	if err != nil {
		t.Fatalf("first SeedDemoFixtures: %v", err)
	}
	second, err := st.SeedDemoFixtures(ctx)
	if err != nil {
		t.Fatalf("second SeedDemoFixtures: %v", err)
	}
	if first.PrimaryOrgID != second.PrimaryOrgID || first.EchoServiceID != second.EchoServiceID || first.EchoRouteID != second.EchoRouteID {
		t.Fatalf("fixtures not idempotent: first=%+v second=%+v", first, second)
	}

	// 2. Tenant isolation: secondary org MUST NOT read primary org's service.
	if _, err := st.GetService(ctx, first.SecondaryOrgID, first.EchoServiceID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for cross-tenant GetService, got: %v", err)
	}

	// Primary org reads its own service.
	svc, err := st.GetService(ctx, first.PrimaryOrgID, first.EchoServiceID)
	if err != nil || svc.Slug != "echo-service" {
		t.Fatalf("unexpected GetService result: svc=%+v err=%v", svc, err)
	}

	// Secondary org ListServices must be empty.
	secServices, err := st.ListServices(ctx, first.SecondaryOrgID, 50, 0)
	if err != nil {
		t.Fatalf("ListServices secondary: %v", err)
	}
	if len(secServices) != 0 {
		t.Fatalf("expected 0 services in secondary org, got %d", len(secServices))
	}

	// 3. Optimistic concurrency on organization update.
	org, err := st.GetOrganization(ctx, first.PrimaryOrgID)
	if err != nil {
		t.Fatalf("GetOrganization: %v", err)
	}
	updated, err := st.UpdateOrganization(ctx, org.ID, "Acme Renamed", true, org.Revision)
	if err != nil || updated.Revision != org.Revision+1 {
		t.Fatalf("UpdateOrganization failed: updated=%+v err=%v", updated, err)
	}
	// Stale revision MUST return ErrConflict.
	if _, err := st.UpdateOrganization(ctx, org.ID, "Stale Write", true, org.Revision); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("expected ErrConflict for stale revision, got: %v", err)
	}

	// 4. Transaction rollback leaves no partial writes.
	sentinel := errors.New("intentional rollback")
	err = st.WithTx(ctx, func(tx *store.Store) error {
		if _, err := tx.CreateOrganization(ctx, "rolled-back-org", "Rolled Back"); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got: %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM organizations WHERE slug = 'rolled-back-org'`).Scan(&count); err != nil {
		t.Fatalf("count rolled-back org: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected rolled-back org count=0, got %d", count)
	}
}
