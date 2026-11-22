package compiler

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/YASSERRMD/GatewayForge/internal/control/store"
)

func TestCompilerDeterministicSerializationAndChecksum(t *testing.T) {
	ts := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)

	inputA := DraftInput{
		OrgID: "org-1",
		Services: map[string]store.Service{
			"svc-1": {ID: "svc-1", Slug: "orders", Enabled: true},
		},
		Pools: []store.UpstreamPool{
			{ID: "pool-b", Name: "B", ConnectTimeoutMS: 2000, ReadTimeoutMS: 5000},
			{ID: "pool-a", Name: "A", ConnectTimeoutMS: 2000, ReadTimeoutMS: 5000},
		},
		TargetsByPool: map[string][]store.UpstreamTarget{
			"pool-a": {
				{ID: "t2", URL: "http://mock-upstream:8089", Weight: 50, Enabled: true},
				{ID: "t1", URL: "http://mock-upstream-2:8089", Weight: 100, Enabled: true},
			},
			"pool-b": {
				{ID: "t3", URL: "http://mock-upstream:8089", Weight: 100, Enabled: true},
			},
		},
		Routes: []store.Route{
			{ID: "r1", ServiceID: "svc-1", UpstreamPoolID: "pool-a", Host: "API.LOCAL", PathPrefix: "/api", Methods: []string{"POST", "GET"}, Priority: 100, Enabled: true},
			{ID: "r2", ServiceID: "svc-1", UpstreamPoolID: "pool-b", Host: "api.local", PathPrefix: "/api/orders", Methods: []string{"GET"}, Priority: 100, Enabled: true},
		},
		Policies: map[string]PolicyRecord{
			"p-org":   {ID: "p-org", Name: "org-rl", PolicyType: "rate_limit", SchemaVersion: 1, Config: json.RawMessage(`{"rpm":100}`), Enabled: true},
			"p-route": {ID: "p-route", Name: "route-key", PolicyType: "api_key", SchemaVersion: 1, Config: json.RawMessage(`{"required":true}`), Enabled: true},
		},
		Attachments: []AttachmentRecord{
			{PolicyID: "p-org", ScopeType: "org", Priority: 10},
			{PolicyID: "p-route", ScopeType: "route", RouteID: "r2", Priority: 10},
		},
	}

	// InputB has identical data in reversed slice orders.
	inputB := DraftInput{
		OrgID:    "org-1",
		Services: inputA.Services,
		Pools:    []store.UpstreamPool{inputA.Pools[1], inputA.Pools[0]},
		TargetsByPool: map[string][]store.UpstreamTarget{
			"pool-a": {inputA.TargetsByPool["pool-a"][1], inputA.TargetsByPool["pool-a"][0]},
			"pool-b": inputA.TargetsByPool["pool-b"],
		},
		Routes:      []store.Route{inputA.Routes[1], inputA.Routes[0]},
		Policies:    inputA.Policies,
		Attachments: []AttachmentRecord{inputA.Attachments[1], inputA.Attachments[0]},
	}

	snapA, err := Compile(inputA, 1, ts)
	if err != nil {
		t.Fatalf("Compile A: %v", err)
	}
	snapB, err := Compile(inputB, 1, ts)
	if err != nil {
		t.Fatalf("Compile B: %v", err)
	}

	if snapA.Checksum == "" || snapA.Checksum != snapB.Checksum {
		t.Fatalf("expected deterministic identical checksums, got A=%q B=%q", snapA.Checksum, snapB.Checksum)
	}
	if err := snapA.VerifyChecksum(); err != nil {
		t.Fatalf("VerifyChecksum failed: %v", err)
	}
	if snapA.Routes[0].ID != "r2" || len(snapA.Routes[0].Policies) != 2 || snapA.Routes[0].Policies[0].ScopeType != "route" {
		t.Fatalf("unexpected route ordering or policy precedence: %+v", snapA.Routes[0])
	}
}
