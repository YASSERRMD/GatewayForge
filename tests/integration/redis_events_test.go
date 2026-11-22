package integration

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/YASSERRMD/GatewayForge/internal/shared/cache"
	"github.com/YASSERRMD/GatewayForge/internal/shared/events"
)

func TestRedisConfigChangeEventsCoalescingAndOrder(t *testing.T) {
	if os.Getenv("INTEGRATION_TESTS") != "1" {
		t.Skip("set INTEGRATION_TESTS=1 to run Redis config-change event integration test")
	}
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "127.0.0.1:6389"
	}
	pw := os.Getenv("REDIS_PASSWORD")
	if pw == "" {
		pw = "gatewayforge_local_redis_pw"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := cache.Connect(ctx, addr, pw)
	if err != nil {
		t.Fatalf("connect redis: %v", err)
	}
	defer func() { _ = client.Close() }()

	channel := "gatewayforge:test:config-events"
	coalescer := events.NewHintCoalescer()

	subCtx, subCancel := context.WithCancel(ctx)
	defer subCancel()
	go events.Subscribe(subCtx, client, channel, coalescer, nil)

	// Allow subscription to attach.
	time.Sleep(150 * time.Millisecond)

	pub := events.NewPublisher(client, channel)

	// 1. Malformed payload is ignored safely.
	_ = client.Publish(ctx, channel, "not-valid-json").Err()

	// 2. Duplicate hints coalesce cleanly.
	hint1 := events.ConfigChangeHint{OrgID: "org-1", Version: 1, Checksum: "aaa111", PublishedAt: time.Now().UTC()}
	if err := pub.PublishHint(ctx, hint1); err != nil {
		t.Fatalf("publish hint1: %v", err)
	}
	if err := pub.PublishHint(ctx, hint1); err != nil {
		t.Fatalf("publish duplicate hint1: %v", err)
	}

	select {
	case got := <-coalescer.Triggers():
		if got.Checksum != "aaa111" || got.Version != 1 {
			t.Fatalf("unexpected hint received: %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for hint1")
	}

	// Duplicate should not trigger a second event.
	select {
	case dup := <-coalescer.Triggers():
		t.Fatalf("duplicate hint should have been deduplicated, got: %+v", dup)
	case <-time.After(150 * time.Millisecond):
	}

	// 3. New checksum triggers immediately (even if versions arrive reordered, gateway still pulls canonical active snapshot).
	hint2 := events.ConfigChangeHint{OrgID: "org-1", Version: 2, Checksum: "bbb222", PublishedAt: time.Now().UTC()}
	if err := pub.PublishHint(ctx, hint2); err != nil {
		t.Fatalf("publish hint2: %v", err)
	}
	select {
	case got := <-coalescer.Triggers():
		if got.Checksum != "bbb222" {
			t.Fatalf("expected checksum bbb222, got %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for hint2")
	}
}
