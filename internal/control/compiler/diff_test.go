package compiler

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/YASSERRMD/GatewayForge/internal/shared/snapshot"
)

func TestDiffSnapshotsSemanticAndNeverExposesSecrets(t *testing.T) {
	base := &snapshot.Snapshot{
		Version: 1,
		Pools: []snapshot.UpstreamPool{
			{ID: "p1", Name: "orders-pool", DestinationClass: "mock_local", ConnectTimeoutMS: 2000, ReadTimeoutMS: 10000},
		},
		Routes: []snapshot.Route{
			{ID: "r1", Name: "Orders", Host: "api.local", PathPrefix: "/v1/orders", Methods: []string{"GET"}, Priority: 100, UpstreamPoolID: "p1"},
			{ID: "r-old", Name: "Legacy", Host: "api.local", PathPrefix: "/v0/legacy", Methods: []string{"GET"}, Priority: 50, UpstreamPoolID: "p1"},
		},
		Consumers: []snapshot.Consumer{
			{
				ID:     "c1",
				Name:   "checkout",
				Status: "active",
				Keys:   []snapshot.APIKey{{ID: "k1", KeyPrefix: "gwf_abc", KeyHash: "SUPER_SECRET_HASH_VALUE_12345"}},
			},
		},
	}

	next := snapshot.Snapshot{
		Version: 2,
		Pools: []snapshot.UpstreamPool{
			{ID: "p1", Name: "orders-pool", DestinationClass: "mock_local", ConnectTimeoutMS: 3000, ReadTimeoutMS: 15000},
		},
		Routes: []snapshot.Route{
			{ID: "r1", Name: "Orders", Host: "api.local", PathPrefix: "/v1/orders", Methods: []string{"GET", "POST"}, Priority: 120, UpstreamPoolID: "p1"},
			{ID: "r-new", Name: "Catalog", Host: "api.local", PathPrefix: "/v1/catalog", Methods: []string{"GET"}, Priority: 100, UpstreamPoolID: "p1"},
		},
		Consumers: []snapshot.Consumer{
			{
				ID:     "c1",
				Name:   "checkout",
				Status: "suspended",
				Keys:   []snapshot.APIKey{{ID: "k1", KeyPrefix: "gwf_abc", KeyHash: "ANOTHER_SECRET_HASH_99999"}},
			},
		},
	}

	diff := DiffSnapshots(base, next)
	if !diff.HasChanges {
		t.Fatal("expected HasChanges=true")
	}
	if diff.BaseVersion != 1 || diff.Summary.Added != 1 || diff.Summary.Removed != 1 || diff.Summary.Modified != 3 {
		t.Fatalf("unexpected diff summary: %+v changes=%+v", diff.Summary, diff.Changes)
	}

	raw, err := json.Marshal(diff)
	if err != nil {
		t.Fatalf("marshal diff: %v", err)
	}
	if bytes.Contains(raw, []byte("SUPER_SECRET_HASH")) || bytes.Contains(raw, []byte("ANOTHER_SECRET_HASH")) {
		t.Fatalf("diff leaked credential hash: %s", string(raw))
	}

	// Identical snapshots yield zero changes
	same := DiffSnapshots(base, *base)
	if same.HasChanges || len(same.Changes) != 0 {
		t.Fatalf("expected identical snapshots to have zero changes, got %+v", same)
	}
}
