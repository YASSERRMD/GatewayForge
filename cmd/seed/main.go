package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/YASSERRMD/GatewayForge/internal/control/compiler"
	"github.com/YASSERRMD/GatewayForge/internal/control/migrate"
	"github.com/YASSERRMD/GatewayForge/internal/control/store"
	"github.com/YASSERRMD/GatewayForge/internal/shared/cache"
	"github.com/YASSERRMD/GatewayForge/internal/shared/db"
	"github.com/YASSERRMD/GatewayForge/internal/shared/events"
	"github.com/YASSERRMD/GatewayForge/internal/shared/netsec"
	"github.com/YASSERRMD/GatewayForge/migrations"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if os.Getenv("GATEWAYFORGE_ENV") == "production" {
		logger.Error("seed CLI is forbidden in production")
		os.Exit(1)
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		logger.Error("DATABASE_URL environment variable is required")
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		logger.Error("connect postgres failed", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	if _, err := migrate.Up(ctx, pool, migrations.Files); err != nil {
		logger.Error("migrate failed", "error", err)
		os.Exit(1)
	}

	st := store.New(pool)
	summary, err := st.SeedDemoFixtures(ctx)
	if err != nil {
		logger.Error("seed fixtures failed", "error", err)
		os.Exit(1)
	}

	pubVersion, pubChecksum, createdPub, err := ensureDemoPublication(ctx, pool, summary.PrimaryOrgID)
	if err != nil {
		logger.Error("seed demo publication failed", "error", err)
		os.Exit(1)
	}

	// Best-effort Redis config-change notification so running gateways converge immediately.
	if redisAddr := os.Getenv("REDIS_ADDR"); redisAddr != "" {
		rc, rErr := cache.Connect(ctx, redisAddr, os.Getenv("REDIS_PASSWORD"))
		if rErr == nil {
			defer func() { _ = rc.Close() }()
			pub := events.NewPublisher(rc, events.ChannelConfigChanged)
			_ = pub.PublishHint(ctx, events.ConfigChangeHint{
				OrgID:       summary.PrimaryOrgID,
				Version:     pubVersion,
				Checksum:    pubChecksum,
				PublishedAt: time.Now().UTC(),
			})
		}
	}

	logger.Info("seed fixtures complete",
		"primary_org_id", summary.PrimaryOrgID,
		"secondary_org_id", summary.SecondaryOrgID,
		"echo_service_id", summary.EchoServiceID,
		"catalog_service_id", summary.CatalogServiceID,
		"echo_route_id", summary.EchoRouteID,
		"catalog_route_id", summary.CatalogRouteID,
		"demo_consumer_id", summary.DemoConsumerID,
		"active_publication_version", pubVersion,
		"publication_checksum", pubChecksum,
		"publication_created", createdPub,
	)
}

func ensureDemoPublication(ctx context.Context, pool *pgxpool.Pool, orgID string) (int, string, bool, error) {
	var existingVersion int
	var existingChecksum string
	err := pool.QueryRow(ctx, `
		SELECT p.version, p.checksum_sha256
		FROM active_publications ap
		JOIN publications p ON p.id = ap.publication_id
		WHERE ap.org_id = $1
	`, orgID).Scan(&existingVersion, &existingChecksum)
	if err == nil {
		return existingVersion, existingChecksum, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, "", false, fmt.Errorf("check active publication: %w", err)
	}

	draft, err := compiler.LoadDraftFromDB(ctx, pool, orgID)
	if err != nil {
		return 0, "", false, fmt.Errorf("load demo draft: %w", err)
	}
	report := compiler.ValidateDraft(draft, netsec.DefaultTargetPolicy(true))
	if !report.Valid {
		return 0, "", false, fmt.Errorf("seeded demo draft failed validation: %+v", report.Errors)
	}

	publishedAt := time.Now().UTC().Truncate(time.Second)
	snap, err := compiler.Compile(draft, 1, publishedAt)
	if err != nil {
		return 0, "", false, fmt.Errorf("compile demo snapshot: %w", err)
	}
	snapJSON, err := json.Marshal(snap)
	if err != nil {
		return 0, "", false, fmt.Errorf("marshal demo snapshot: %w", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, "", false, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var pubID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO publications (org_id, version, draft_revision, snapshot_json, checksum_sha256, notes, published_at)
		VALUES ($1, 1, $2, $3, $4, 'Initial seeded demo release', $5)
		RETURNING id
	`, orgID, draft.DraftRevision, snapJSON, snap.Checksum, publishedAt).Scan(&pubID); err != nil {
		return 0, "", false, fmt.Errorf("insert demo publication: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO active_publications (org_id, publication_id, activation_revision, activated_at)
		VALUES ($1, $2, 1, NOW())
	`, orgID, pubID); err != nil {
		return 0, "", false, fmt.Errorf("activate demo publication: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO publication_activations (org_id, publication_id, is_rollback, reason)
		VALUES ($1, $2, FALSE, 'Initial seeded demo release')
	`, orgID, pubID); err != nil {
		return 0, "", false, fmt.Errorf("log demo activation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, "", false, fmt.Errorf("commit demo publication: %w", err)
	}
	return 1, snap.Checksum, true, nil
}
