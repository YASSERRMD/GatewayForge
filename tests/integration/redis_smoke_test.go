package integration

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/YASSERRMD/GatewayForge/internal/shared/cache"
)

func TestRedisConnectivitySmoke(t *testing.T) {
	if os.Getenv("INTEGRATION_TESTS") != "1" {
		t.Skip("set INTEGRATION_TESTS=1 to run Redis integration smoke test")
	}
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "127.0.0.1:6389"
	}
	pw := os.Getenv("REDIS_PASSWORD")
	if pw == "" {
		pw = "gatewayforge_local_redis_pw"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, err := cache.Connect(ctx, addr, pw)
	if err != nil {
		t.Fatalf("connect redis: %v", err)
	}
	defer func() { _ = client.Close() }()

	if err := client.Set(ctx, "gatewayforge:smoke", "ok", 10*time.Second).Err(); err != nil {
		t.Fatalf("redis SET failed: %v", err)
	}
	val, err := client.Get(ctx, "gatewayforge:smoke").Result()
	if err != nil || val != "ok" {
		t.Fatalf("redis GET unexpected: val=%q err=%v", val, err)
	}
	_ = client.Del(ctx, "gatewayforge:smoke").Err()
}
