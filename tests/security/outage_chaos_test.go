package security

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/YASSERRMD/GatewayForge/internal/gateway/ratelimit"
	syncer "github.com/YASSERRMD/GatewayForge/internal/gateway/sync"
	"github.com/YASSERRMD/GatewayForge/internal/gateway/table"
	"github.com/YASSERRMD/GatewayForge/internal/shared/policy"
	"github.com/YASSERRMD/GatewayForge/internal/shared/snapshot"
)

type toggleFaultLimiter struct {
	down  atomic.Bool
	inner ratelimit.Limiter
}

func (f *toggleFaultLimiter) Allow(ctx context.Context, key string, pol policy.RateLimitPolicy) (ratelimit.Decision, error) {
	if f.down.Load() {
		return ratelimit.Decision{}, errors.New("redis connection refused (simulated outage)")
	}
	return f.inner.Allow(ctx, key, pol)
}

func TestRedisOutageFailOpenVsFailClosedAndRecovery(t *testing.T) {
	primary := &toggleFaultLimiter{inner: ratelimit.NewLocalLimiter(nil, 1000)}
	enforcer := ratelimit.NewEnforcer(primary, ratelimit.NewLocalLimiter(nil, 1000), nil, nil)

	openCfg, _ := json.Marshal(policy.RateLimitPolicy{
		KeyBy: "consumer", RequestsPerWindow: 100, WindowSeconds: 60, Burst: 100,
		SecurityClass: policy.ClassStandard, FailMode: policy.FailOpen,
	})
	closedCfg, _ := json.Marshal(policy.RateLimitPolicy{
		KeyBy: "consumer", RequestsPerWindow: 100, WindowSeconds: 60, Burst: 100,
		SecurityClass: policy.ClassCritical, FailMode: policy.FailClosed,
	})

	routeOpen := snapshot.Route{
		ID: "r-open",
		Policies: []snapshot.AttachedPolicy{
			{ID: "p-open", Name: "std-rl", PolicyType: "rate_limit", SchemaVersion: 1, Config: openCfg},
		},
	}
	routeClosed := snapshot.Route{
		ID: "r-closed",
		Policies: []snapshot.AttachedPolicy{
			{ID: "p-closed", Name: "crit-rl", PolicyType: "rate_limit", SchemaVersion: 1, Config: closedCfg},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/orders", nil)

	// 1. Healthy Redis -> both routes allowed
	if res := enforcer.Enforce(context.Background(), req, "org-1", routeOpen, "c1"); !res.Allowed {
		t.Fatalf("healthy standard route expected allowed, got %+v", res)
	}
	if res := enforcer.Enforce(context.Background(), req, "org-1", routeClosed, "c1"); !res.Allowed {
		t.Fatalf("healthy critical route expected allowed, got %+v", res)
	}

	// 2. Redis outage -> standard fail_open degrades open, critical fail_closed MUST reject!
	primary.down.Store(true)
	resOpen := enforcer.Enforce(context.Background(), req, "org-1", routeOpen, "c1")
	if !resOpen.Allowed || !resOpen.DegradedOpen {
		t.Fatalf("fail_open route during Redis outage expected Allowed=true DegradedOpen=true, got %+v", resOpen)
	}
	resClosed := enforcer.Enforce(context.Background(), req, "org-1", routeClosed, "c1")
	if resClosed.Allowed || resClosed.DeniedBy != "backend_unavailable" {
		t.Fatalf("critical fail_closed route during Redis outage MUST deny with backend_unavailable, got %+v", resClosed)
	}

	// 3. Redis recovers -> critical route recovers automatically
	primary.down.Store(false)
	resRecovered := enforcer.Enforce(context.Background(), req, "org-1", routeClosed, "c1")
	if !resRecovered.Allowed || resRecovered.DegradedOpen {
		t.Fatalf("critical route after Redis recovery expected Allowed=true DegradedOpen=false, got %+v", resRecovered)
	}
}

func TestControlPlaneOutagePreservesLKGAndPeriodicReconciliationRecovers(t *testing.T) {
	var cpDown atomic.Bool
	var servedVersion atomic.Int32
	servedVersion.Store(1)

	makeSnap := func(ver int) snapshot.Snapshot {
		s := snapshot.Snapshot{
			SchemaVersion: snapshot.SchemaVersion,
			OrgID:         "org-chaos",
			Version:       ver,
			PublishedAt:   time.Now().UTC().Truncate(time.Second),
			Pools: []snapshot.UpstreamPool{
				{ID: "p1", Name: "pool", Targets: []snapshot.UpstreamTarget{{ID: "t1", URL: "http://mock-upstream:8089", Weight: 100, Enabled: true}}},
			},
			Routes: []snapshot.Route{
				{ID: "r1", OrgID: "org-chaos", UpstreamPoolID: "p1", Name: "orders", Host: "chaos.local", PathPrefix: "/v1/orders", Methods: []string{"GET"}, Priority: 100, Enabled: true},
			},
		}
		sum, _ := s.ComputeChecksum()
		s.Checksum = sum
		return s
	}

	cpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cpDown.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		snap := makeSnap(int(servedVersion.Load()))
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", `"`+snap.Checksum+`"`)
		_ = json.NewEncoder(w).Encode(snap)
	}))
	defer cpServer.Close()

	lkgPath := filepath.Join(t.TempDir(), "lkg-snapshot.json")
	holder := table.NewHolder()
	poller := syncer.NewPoller(holder, syncer.PollerConfig{
		ControlPlaneURL: cpServer.URL,
		SyncToken:       "sync-token",
		OrgID:           "org-chaos",
		LKGPath:         lkgPath,
		Interval:        25 * time.Millisecond,
	})

	// 1. Initial sync loads v1 & persists LKG to disk
	if err := poller.SyncOnce(context.Background()); err != nil {
		t.Fatalf("initial sync v1 failed: %v", err)
	}
	if cur := holder.Current(); cur == nil || cur.Version != 1 {
		t.Fatalf("expected active v1, got %+v", cur)
	}

	// 2. Control plane / DB outage -> sync errors recorded, LKG v1 preserved intact
	cpDown.Store(true)
	if err := poller.SyncOnce(context.Background()); err == nil {
		t.Fatal("expected sync error while control plane is down")
	}
	if cur := holder.Current(); cur == nil || cur.Version != 1 {
		t.Fatalf("expected LKG v1 preserved during outage, got %+v", cur)
	}

	// 3. Control plane recovers with v2 (no Redis pub/sub hint) -> periodic reconciliation converges to v2!
	servedVersion.Store(2)
	cpDown.Store(false)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go poller.Run(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cur := holder.Current(); cur != nil && cur.Version == 2 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("expected periodic reconciliation to converge to v2 after recovery, current=%+v", holder.Current())
}
