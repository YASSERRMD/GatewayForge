package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/YASSERRMD/GatewayForge/internal/control/migrate"
	"github.com/YASSERRMD/GatewayForge/internal/shared/db"
	"github.com/YASSERRMD/GatewayForge/migrations"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		logger.Error("DATABASE_URL environment variable is required")
		os.Exit(1)
	}

	action := "up"
	if len(os.Args) > 1 {
		action = os.Args[1]
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		logger.Error("failed to connect to PostgreSQL", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	switch action {
	case "up":
		applied, err := migrate.Up(ctx, pool, migrations.Files)
		if err != nil {
			logger.Error("migration failed", "error", err)
			os.Exit(1)
		}
		logger.Info("migrations complete", "newly_applied", applied)
	case "status":
		records, err := migrate.Status(ctx, pool)
		if err != nil {
			logger.Error("failed to read migration status", "error", err)
			os.Exit(1)
		}
		for _, r := range records {
			fmt.Printf("%04d  %-40s  %s  %s\n", r.Version, r.Name, r.Checksum[:12], r.AppliedAt.Format(time.RFC3339))
		}
	default:
		logger.Error("unknown migrate command; expected 'up' or 'status'", "command", action)
		os.Exit(1)
	}
}
