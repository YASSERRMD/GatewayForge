package compiler

import (
	"encoding/json"
	"testing"

	"github.com/YASSERRMD/GatewayForge/internal/control/store"
	"github.com/YASSERRMD/GatewayForge/internal/shared/netsec"
)

func TestValidateDraftExhaustiveChecks(t *testing.T) {
	pol := netsec.DefaultTargetPolicy(true)

	t.Run("valid draft passes with zero errors", func(t *testing.T) {
		in := DraftInput{
			OrgID:         "org-1",
			DraftRevision: 3,
			Services:      map[string]store.Service{"s1": {ID: "s1", Slug: "echo", Enabled: true}},
			Pools:         []store.UpstreamPool{{ID: "p1", Name: "echo-pool", ConnectTimeoutMS: 2000, ReadTimeoutMS: 5000}},
			TargetsByPool: map[string][]store.UpstreamTarget{
				"p1": {{ID: "t1", URL: "http://mock-upstream:8089", Weight: 100, Enabled: true}},
			},
			Routes: []store.Route{
				{ID: "r1", Name: "echo", ServiceID: "s1", UpstreamPoolID: "p1", Host: "api.local", PathPrefix: "/echo", Methods: []string{"GET"}, Priority: 100, Enabled: true},
			},
			Policies: map[string]PolicyRecord{
				"pol1": {ID: "pol1", Name: "rl", PolicyType: "rate_limit", SchemaVersion: 1, Config: json.RawMessage(`{"requests_per_window":60,"window_seconds":60,"key_by":"consumer"}`), Enabled: true},
			},
			Attachments: []AttachmentRecord{{PolicyID: "pol1", ScopeType: "route", RouteID: "r1"}},
		}
		rep := ValidateDraft(in, pol)
		if !rep.Valid || len(rep.Errors) != 0 {
			t.Fatalf("expected valid draft, got %+v", rep)
		}
	})

	t.Run("detects SSRF target, empty pool, dangling refs, route conflict, and bad policy", func(t *testing.T) {
		in := DraftInput{
			OrgID:         "org-1",
			DraftRevision: 4,
			Services:      map[string]store.Service{"s1": {ID: "s1", Slug: "echo", Enabled: true}},
			Pools: []store.UpstreamPool{
				{ID: "p-bad", Name: "bad-pool", ConnectTimeoutMS: 10, ReadTimeoutMS: 5000},
				{ID: "p-empty", Name: "empty-pool", ConnectTimeoutMS: 1000, ReadTimeoutMS: 5000},
			},
			TargetsByPool: map[string][]store.UpstreamTarget{
				"p-bad": {{ID: "t1", URL: "http://169.254.169.254/latest", Weight: 0, Enabled: true}},
			},
			Routes: []store.Route{
				{ID: "r1", Name: "r1", ServiceID: "s1", UpstreamPoolID: "p-empty", Host: "api.local", PathPrefix: "/dup", Methods: []string{"GET"}, Priority: 100, Enabled: true},
				{ID: "r2", Name: "r2", ServiceID: "missing-svc", UpstreamPoolID: "missing-pool", Host: "api.local", PathPrefix: "/dup", Methods: []string{"GET"}, Priority: 100, Enabled: true},
			},
			Policies: map[string]PolicyRecord{
				"pol-bad": {ID: "pol-bad", Name: "bad", PolicyType: "wasm", SchemaVersion: 2, Config: json.RawMessage(`"not-object"`), Enabled: true},
			},
			Attachments: []AttachmentRecord{
				{PolicyID: "missing-pol", ScopeType: "org"},
				{PolicyID: "pol-bad", ScopeType: "route", RouteID: "missing-route"},
			},
		}

		rep := ValidateDraft(in, pol)
		if rep.Valid {
			t.Fatal("expected invalid draft report")
		}
		codes := make(map[string]bool)
		for _, e := range rep.Errors {
			codes[e.Code] = true
		}
		for _, want := range []string{
			"disallowed_target_url",
			"invalid_timeout",
			"invalid_weight",
			"route_pool_empty",
			"dangling_service_ref",
			"dangling_pool_ref",
			"route_conflict",
			"unknown_policy_type",
			"unsupported_policy_version",
			"invalid_policy_config",
			"dangling_policy_attachment",
		} {
			if !codes[want] {
				t.Errorf("expected error code %q in report, got codes=%v", want, codes)
			}
		}
	})
}
