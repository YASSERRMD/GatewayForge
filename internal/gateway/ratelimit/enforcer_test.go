package ratelimit

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/YASSERRMD/GatewayForge/internal/shared/policy"
	"github.com/YASSERRMD/GatewayForge/internal/shared/snapshot"
)

type failingLimiter struct{ err error }

func (f failingLimiter) Allow(context.Context, string, policy.RateLimitPolicy) (Decision, error) {
	return Decision{}, f.err
}

func TestFailModeBehaviorDuringRedisOutage(t *testing.T) {
	outage := failingLimiter{err: errors.New("dial tcp 127.0.0.1:6389: connection refused")}
	enf := NewEnforcer(outage, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "http://api.local/v1/items", nil)
	req.RemoteAddr = "198.51.100.10:12345"

	t.Run("standard fail_open allows traffic and marks degraded", func(t *testing.T) {
		cfg, _ := json.Marshal(policy.RateLimitPolicy{
			KeyBy:             policy.KeyByIP,
			RequestsPerWindow: 10,
			WindowSeconds:     60,
			FailMode:          policy.FailOpen,
			SecurityClass:     policy.ClassStandard,
		})
		rt := snapshot.Route{
			ID: "rt-open",
			Policies: []snapshot.AttachedPolicy{{
				ID: "p-open", PolicyType: "rate_limit", ScopeType: "route", Config: cfg,
			}},
		}
		res := enf.Enforce(context.Background(), req, "org-1", rt, "")
		if !res.Allowed || !res.DegradedOpen {
			t.Fatalf("expected fail_open degraded allow, got %+v", res)
		}
	})

	t.Run("fail_closed blocks traffic with backend_unavailable", func(t *testing.T) {
		cfg, _ := json.Marshal(policy.RateLimitPolicy{
			KeyBy:             policy.KeyByIP,
			RequestsPerWindow: 10,
			WindowSeconds:     60,
			FailMode:          policy.FailClosed,
			SecurityClass:     policy.ClassStandard,
		})
		rt := snapshot.Route{
			ID: "rt-closed",
			Policies: []snapshot.AttachedPolicy{{
				ID: "p-closed", PolicyType: "rate_limit", ScopeType: "route", Config: cfg,
			}},
		}
		res := enf.Enforce(context.Background(), req, "org-1", rt, "")
		if res.Allowed || res.DeniedBy != "backend_unavailable" || !errors.Is(res.BackendError, ErrLimiterUnavailable) {
			t.Fatalf("expected fail_closed rejection, got %+v", res)
		}
	})

	t.Run("critical security class NEVER fails open even if fail_mode omitted", func(t *testing.T) {
		cfg := json.RawMessage(`{"key_by":"consumer","requests_per_window":5,"window_seconds":60,"security_class":"critical"}`)
		rt := snapshot.Route{
			ID: "rt-crit",
			Policies: []snapshot.AttachedPolicy{{
				ID: "p-crit", PolicyType: "rate_limit", ScopeType: "route", Config: cfg,
			}},
		}
		res := enf.Enforce(context.Background(), req, "org-1", rt, "cons-1")
		if res.Allowed || res.DeniedBy != "backend_unavailable" {
			t.Fatalf("critical route must fail closed on outage, got %+v", res)
		}
	})
}
