package integration

import (
	"context"
	"testing"
	"time"

	"github.com/YASSERRMD/GatewayForge/internal/control/compiler"
	"github.com/YASSERRMD/GatewayForge/internal/control/store"
	"github.com/YASSERRMD/GatewayForge/internal/shared/netsec"
)

func TestRealisticSeedDemoIdempotencyAndDraftValidity(t *testing.T) {
	pool := openIntegrationDB(t)
	resetAndMigrateDB(t, pool)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	st := store.New(pool)

	first, err := st.SeedDemoFixtures(ctx)
	if err != nil {
		t.Fatalf("first SeedDemoFixtures: %v", err)
	}
	second, err := st.SeedDemoFixtures(ctx)
	if err != nil {
		t.Fatalf("second SeedDemoFixtures: %v", err)
	}

	if first != second {
		t.Fatalf("SeedDemoFixtures is not idempotent:\nfirst=%+v\nsecond=%+v", first, second)
	}

	// Verify expected realistic entity counts with zero duplication.
	var svcCount, routeCount, policyCount, attachCount, consumerCount, keyCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM services WHERE org_id = $1 AND deleted_at IS NULL`, first.PrimaryOrgID).Scan(&svcCount); err != nil {
		t.Fatalf("count services: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM routes WHERE org_id = $1`, first.PrimaryOrgID).Scan(&routeCount); err != nil {
		t.Fatalf("count routes: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM policies WHERE org_id = $1`, first.PrimaryOrgID).Scan(&policyCount); err != nil {
		t.Fatalf("count policies: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM policy_attachments WHERE org_id = $1`, first.PrimaryOrgID).Scan(&attachCount); err != nil {
		t.Fatalf("count policy_attachments: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM consumers WHERE org_id = $1`, first.PrimaryOrgID).Scan(&consumerCount); err != nil {
		t.Fatalf("count consumers: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM api_keys WHERE org_id = $1`, first.PrimaryOrgID).Scan(&keyCount); err != nil {
		t.Fatalf("count api_keys: %v", err)
	}

	if svcCount != 2 || routeCount != 2 || policyCount != 3 || attachCount != 3 || consumerCount != 1 {
		t.Fatalf("unexpected seeded counts: services=%d routes=%d policies=%d attachments=%d consumers=%d",
			svcCount, routeCount, policyCount, attachCount, consumerCount)
	}
	if keyCount != 0 {
		t.Fatalf("expected 0 pre-seeded plaintext/static API keys, got %d", keyCount)
	}

	// Seeded demo draft must validate cleanly under non-production mock target policy and compile cleanly.
	draft, err := compiler.LoadDraftFromDB(ctx, pool, first.PrimaryOrgID)
	if err != nil {
		t.Fatalf("LoadDraftFromDB: %v", err)
	}
	report := compiler.ValidateDraft(draft, netsec.DefaultTargetPolicy(true))
	if !report.Valid {
		t.Fatalf("seeded draft failed validation: %+v", report.Errors)
	}
	snap, err := compiler.Compile(draft, 1, time.Now().UTC().Truncate(time.Second))
	if err != nil {
		t.Fatalf("Compile seeded draft: %v", err)
	}
	if len(snap.Routes) != 2 || len(snap.Checksum) != 64 {
		t.Fatalf("unexpected compiled demo snapshot: routes=%d checksum=%q", len(snap.Routes), snap.Checksum)
	}
}
