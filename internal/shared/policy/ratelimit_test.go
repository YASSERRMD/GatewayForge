package policy

import (
	"encoding/json"
	"testing"
)

func TestRateLimitPolicyValidationAndDefaults(t *testing.T) {
	t.Run("valid policy applies safe defaults", func(t *testing.T) {
		p, err := ParseRateLimitPolicy(json.RawMessage(`{"requests_per_window":60,"window_seconds":60}`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p.Algorithm != AlgoTokenBucket || p.KeyBy != KeyByConsumer || p.Burst != 60 || p.FailMode != FailOpen || !p.EmitHeaders() {
			t.Fatalf("unexpected defaults: %+v", p)
		}
	})

	t.Run("critical security class defaults to fail_closed and rejects fail_open", func(t *testing.T) {
		p, err := ParseRateLimitPolicy(json.RawMessage(`{"requests_per_window":10,"window_seconds":60,"security_class":"critical"}`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p.FailMode != FailClosed {
			t.Fatalf("expected fail_closed default for critical class, got %q", p.FailMode)
		}
		if _, err := ParseRateLimitPolicy(json.RawMessage(`{"requests_per_window":10,"window_seconds":60,"security_class":"critical","fail_mode":"fail_open"}`)); err == nil {
			t.Fatal("expected critical+fail_open to be rejected")
		}
	})

	cases := []struct {
		name string
		raw  string
	}{
		{"zero requests", `{"requests_per_window":0,"window_seconds":60}`},
		{"huge requests", `{"requests_per_window":5000000,"window_seconds":60}`},
		{"zero window", `{"requests_per_window":10,"window_seconds":0}`},
		{"burst below requests", `{"requests_per_window":20,"window_seconds":60,"burst":5}`},
		{"bad algo", `{"algorithm":"leaky","requests_per_window":10,"window_seconds":60}`},
		{"bad key_by", `{"key_by":"query","requests_per_window":10,"window_seconds":60}`},
		{"header without name", `{"key_by":"header","requests_per_window":10,"window_seconds":60}`},
		{"bad fail_mode", `{"fail_mode":"maybe","requests_per_window":10,"window_seconds":60}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseRateLimitPolicy(json.RawMessage(tc.raw)); err == nil {
				t.Fatalf("expected validation error for %s", tc.name)
			}
		})
	}

	t.Run("config digest changes when limit changes", func(t *testing.T) {
		p1, _ := ParseRateLimitPolicy(json.RawMessage(`{"requests_per_window":10,"window_seconds":60,"burst":10}`))
		p2, _ := ParseRateLimitPolicy(json.RawMessage(`{"requests_per_window":5,"window_seconds":60,"burst":5}`))
		if p1.ConfigDigest() == p2.ConfigDigest() {
			t.Fatalf("expected different digests when limit changes: %s", p1.ConfigDigest())
		}
	})
}

func TestQuotaPolicyValidation(t *testing.T) {
	q, err := ParseQuotaPolicy(json.RawMessage(`{"limit":1000,"period":"daily","key_by":"consumer"}`))
	if err != nil {
		t.Fatalf("unexpected quota error: %v", err)
	}
	if q.FailMode != FailOpen || q.ConfigDigest() == "" {
		t.Fatalf("unexpected quota defaults: %+v", q)
	}
	if _, err := ParseQuotaPolicy(json.RawMessage(`{"limit":0,"period":"daily"}`)); err == nil {
		t.Fatal("expected zero quota limit error")
	}
	if _, err := ParseQuotaPolicy(json.RawMessage(`{"limit":100,"period":"weekly"}`)); err == nil {
		t.Fatal("expected unsupported period error")
	}
	if _, err := ParseQuotaPolicy(json.RawMessage(`{"limit":100,"period":"daily","security_class":"critical","fail_mode":"fail_open"}`)); err == nil {
		t.Fatal("expected critical quota fail_open rejection")
	}
}
