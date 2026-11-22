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

func TestTraceCorrelationStoreTenantIsolation(t *testing.T) {
	if os.Getenv("INTEGRATION_TESTS") != "1" {
		t.Skip("set INTEGRATION_TESTS=1 to run correlation Redis test")
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

	prefix := fmt.Sprintf("gwf:test:corr:%d:", time.Now().UnixNano())
	cs := telemetry.NewCorrelationStore(rc, prefix, 5*time.Minute)

	const reqID = "req_trace_e2e_001"
	const traceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	if err := cs.Record(ctx, telemetry.RequestCorrelation{
		RequestID:  reqID,
		TraceID:    traceID,
		OrgID:      "org-alpha",
		RouteID:    "rt-1",
		ServiceID:  "svc-1",
		Method:     "GET",
		Status:     200,
		Outcome:    "ok",
		DurationMS: 4.5,
		Timestamp:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	// Same org resolves cleanly.
	rec, err := cs.Lookup(ctx, "org-alpha", reqID)
	if err != nil || rec == nil || rec.TraceID != traceID {
		t.Fatalf("org-alpha lookup unexpected: rec=%+v err=%v", rec, err)
	}
	if link, ok := telemetry.BuildTraceViewerURL("http://localhost:16686/trace", rec.TraceID); !ok || link != "http://localhost:16686/trace/"+traceID {
		t.Fatalf("unexpected trace viewer link: ok=%v link=%q", ok, link)
	}

	// Different org MUST NOT see org-alpha's correlation record!
	other, err := cs.Lookup(ctx, "org-beta", reqID)
	if err != nil || other != nil {
		t.Fatalf("cross-org correlation leak: other=%+v err=%v", other, err)
	}
}
