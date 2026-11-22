package ratelimit

import (
	"context"
	"testing"
	"time"

	"github.com/YASSERRMD/GatewayForge/internal/shared/policy"
)

func TestLocalQuotaUTCPeriodRolloverAndClockSkew(t *testing.T) {
	// 23:59:50 UTC on Nov 22 -> 10 seconds before daily rollover to Nov 23.
	now := time.Date(2026, 11, 22, 23, 59, 50, 0, time.UTC)
	qc := NewLocalQuotaCounter(func() time.Time { return now }, 100)

	q := policy.QuotaPolicy{KeyBy: policy.KeyByConsumer, Limit: 2, Period: policy.PeriodDaily}
	in := KeyInput{OrgID: "org-1", PolicyID: "q1", ScopeType: "org", ConsumerID: "cons-1"}

	d1, err := qc.Consume(context.Background(), in, q)
	if err != nil || !d1.Allowed || d1.Remaining != 1 {
		t.Fatalf("req 1: %+v err=%v", d1, err)
	}
	if d1.ResetAfter != 10*time.Second {
		t.Fatalf("resetAfter=%v want 10s", d1.ResetAfter)
	}

	d2, _ := qc.Consume(context.Background(), in, q)
	if !d2.Allowed || d2.Remaining != 0 {
		t.Fatalf("req 2: %+v", d2)
	}

	// 3rd request denied until midnight UTC (10s).
	d3, _ := qc.Consume(context.Background(), in, q)
	if d3.Allowed || d3.RetryAfter != 10*time.Second {
		t.Fatalf("req 3 expected deny with retryAfter=10s, got %+v", d3)
	}

	// Small backward clock skew within the same UTC day still stays denied.
	now = now.Add(-3 * time.Second)
	dSkew, _ := qc.Consume(context.Background(), in, q)
	if dSkew.Allowed {
		t.Fatal("backward clock skew within same UTC day must stay denied")
	}

	// Roll over to 2026-11-23T00:00:00Z -> fresh daily bucket!
	now = time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
	dNext, _ := qc.Consume(context.Background(), in, q)
	if !dNext.Allowed || dNext.Remaining != 1 || dNext.ResetAfter != 24*time.Hour {
		t.Fatalf("expected fresh daily quota after UTC rollover, got %+v", dNext)
	}
}

func TestUTCPeriodWindowSemantics(t *testing.T) {
	// Non-UTC input timezone must be normalized strictly to UTC.
	locPlus4 := time.FixedZone("UTC+4", 4*3600)
	localTime := time.Date(2026, 11, 23, 1, 30, 0, 0, locPlus4) // 2026-11-22 21:30:00 UTC

	dayBucket, _, rem, err := UTCPeriodWindow(localTime, policy.PeriodDaily)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if dayBucket != "20261122" {
		t.Fatalf("dayBucket=%q want 20261122 (UTC)", dayBucket)
	}
	if rem != 2*time.Hour+30*time.Minute {
		t.Fatalf("remaining=%v want 2h30m", rem)
	}

	monBucket, win, _, err := UTCPeriodWindow(localTime, policy.PeriodMonthly)
	if err != nil || monBucket != "202611" || win != 30*24*time.Hour {
		t.Fatalf("monthly bucket=%q win=%v err=%v", monBucket, win, err)
	}
}
