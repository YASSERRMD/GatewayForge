package integration

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/YASSERRMD/GatewayForge/internal/shared/cache"
	"github.com/YASSERRMD/GatewayForge/internal/shared/telemetry"
)

func TestGatewayInstanceHeartbeatConvergenceAndTenantFilter(t *testing.T) {
	if os.Getenv("INTEGRATION_TESTS") != "1" {
		t.Skip("set INTEGRATION_TESTS=1 to run heartbeat Redis integration test")
	}
	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "127.0.0.1:6389"
	}
	redisPw := os.Getenv("REDIS_PASSWORD")
	if redisPw == "" {
		redisPw = "gatewayforge_local_redis_pw"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	rc, err := cache.Connect(ctx, redisAddr, redisPw)
	if err != nil {
		t.Fatalf("connect redis: %v", err)
	}
	defer func() { _ = rc.Close() }()

	prefix := fmt.Sprintf("gwf:test:hb:%d:", time.Now().UnixNano())
	hb := telemetry.NewHeartbeatStore(rc, prefix)

	_ = hb.Publish(ctx, telemetry.GatewayHeartbeat{
		InstanceID: "gw-a1", OrgID: "org-alpha", ActiveVersion: 3, Checksum: "sha-v3", Ready: true, LastSeenAt: time.Now().UTC(),
	})
	_ = hb.Publish(ctx, telemetry.GatewayHeartbeat{
		InstanceID: "gw-a2", OrgID: "org-alpha", ActiveVersion: 2, Checksum: "sha-v2", Ready: true, LastSeenAt: time.Now().UTC().Add(-45 * time.Second),
	})
	_ = hb.Publish(ctx, telemetry.GatewayHeartbeat{
		InstanceID: "gw-b1", OrgID: "org-beta", ActiveVersion: 3, Checksum: "sha-v3", Ready: true, LastSeenAt: time.Now().UTC(),
	})
	defer func() {
		_ = rc.Del(context.Background(), prefix+"gw-a1", prefix+"gw-a2", prefix+"gw-b1").Err()
	}()

	list, err := hb.ListForOrg(ctx, "org-alpha", 3, "sha-v3")
	if err != nil {
		t.Fatalf("ListForOrg: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 alpha instances (beta filtered out), got %d: %+v", len(list), list)
	}
	if list[0].InstanceID != "gw-a1" || list[0].Status != "healthy" || !list[0].Converged {
		t.Fatalf("unexpected gw-a1 status: %+v", list[0])
	}
	if list[1].InstanceID != "gw-a2" || list[1].Status != "stale" || list[1].Converged {
		t.Fatalf("unexpected gw-a2 status: %+v", list[1])
	}
}
