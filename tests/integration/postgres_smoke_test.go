package integration

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/YASSERRMD/GatewayForge/internal/shared/db"
)

func TestPostgresConnectivitySmoke(t *testing.T) {
	if os.Getenv("INTEGRATION_TESTS") != "1" {
		t.Skip("set INTEGRATION_TESTS=1 to run PostgreSQL integration smoke test")
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://gatewayforge:gatewayforge_local_dev_pw@127.0.0.1:5439/gatewayforge?sslmode=disable"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	defer pool.Close()

	var one int
	if err := pool.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil {
		t.Fatalf("SELECT 1 failed: %v", err)
	}
	if one != 1 {
		t.Fatalf("expected 1, got %d", one)
	}
}
