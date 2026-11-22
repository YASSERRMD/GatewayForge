package table

import (
	"fmt"
	"sync"
	"testing"

	"github.com/YASSERRMD/GatewayForge/internal/shared/snapshot"
)

func makeValidSnapshot(version int, pathPrefix string) snapshot.Snapshot {
	snap := snapshot.Snapshot{
		SchemaVersion: snapshot.SchemaVersion,
		OrgID:         "org-1",
		Version:       version,
		Pools: []snapshot.UpstreamPool{
			{
				ID:   "pool-1",
				Name: "echo",
				Targets: []snapshot.UpstreamTarget{
					{ID: "t-1", URL: "http://mock-upstream:8089", Weight: 100, Enabled: true},
				},
			},
		},
		Routes: []snapshot.Route{
			{
				ID:             fmt.Sprintf("route-v%d", version),
				OrgID:          "org-1",
				UpstreamPoolID: "pool-1",
				Host:           "api.local",
				PathPrefix:     pathPrefix,
				Methods:        []string{"GET"},
				Priority:       100,
				Enabled:        true,
			},
		},
	}
	sum, _ := snap.ComputeChecksum()
	snap.Checksum = sum
	return snap
}

func TestImmutableTableSwapAndRaceSafety(t *testing.T) {
	holder := NewHolder()

	// Empty initial table rejects everything.
	if res := holder.Current().Matcher.Match("api.local", "GET", "/v1"); res.Matched {
		t.Fatal("empty initial table must not match routes")
	}

	// Apply v1 and hold pointer simulating an in-flight request.
	v1Snap := makeValidSnapshot(1, "/v1")
	if _, err := holder.Apply(v1Snap, true); err != nil {
		t.Fatalf("apply v1: %v", err)
	}
	inFlight := holder.Current()

	// Bad snapshot (dangling pool) MUST fail and keep v1 active.
	bad := makeValidSnapshot(2, "/v2")
	bad.Routes[0].UpstreamPoolID = "missing-pool"
	bad.Checksum, _ = bad.ComputeChecksum()
	if _, err := holder.Apply(bad, true); err == nil {
		t.Fatal("expected error for dangling upstream_pool_id")
	}
	if holder.Current().Version != 1 || holder.LastError() == "" {
		t.Fatalf("expected last-known-good v1 preserved, got ver=%d err=%q", holder.Current().Version, holder.LastError())
	}

	// Bad checksum MUST fail.
	tampered := makeValidSnapshot(2, "/v2")
	tampered.Checksum = "deadbeef"
	if _, err := holder.Apply(tampered, true); err == nil {
		t.Fatal("expected checksum mismatch error")
	}

	// Swap to v2; inFlight pointer MUST still observe v1 immutably!
	if _, err := holder.Apply(makeValidSnapshot(2, "/v2"), true); err != nil {
		t.Fatalf("apply v2: %v", err)
	}
	if !inFlight.Matcher.Match("api.local", "GET", "/v1").Matched || inFlight.Matcher.Match("api.local", "GET", "/v2").Matched {
		t.Fatal("in-flight table pointer was mutated after swap")
	}
	if !holder.Current().Matcher.Match("api.local", "GET", "/v2").Matched {
		t.Fatal("current table did not switch to v2")
	}

	// Hammer concurrent readers and writers under -race.
	var wg sync.WaitGroup
	for r := 0; r < 12; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				tbl := holder.Current()
				_ = tbl.Matcher.Match("api.local", "GET", "/v2")
				_ = tbl.Version
			}
		}()
	}
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 3; i < 60; i++ {
				_, _ = holder.Apply(makeValidSnapshot(worker*100+i, "/v2"), true)
			}
		}(w)
	}
	wg.Wait()
}
