package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/YASSERRMD/GatewayForge/internal/control/migrate"
	"github.com/YASSERRMD/GatewayForge/internal/control/store"
	"github.com/YASSERRMD/GatewayForge/internal/shared/db"
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

	logger.Info("seed fixtures complete",
		"primary_org_id", summary.PrimaryOrgID,
		"secondary_org_id", summary.SecondaryOrgID,
		"echo_service_id", summary.EchoServiceID,
		"echo_route_id", summary.EchoRouteID,
	)
}
