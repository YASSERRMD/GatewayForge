package resilience

import (
	"context"
	"testing"
	"time"

	"github.com/YASSERRMD/GatewayForge/internal/shared/policy"
)

func TestBoundedExponentialBackoffAndDeadlineBudget(t *testing.T) {
	pol := policy.RetryPolicy{
		MaxAttempts:    5,
		BaseBackoffMS:  100,
		MaxBackoffMS:   500,
		JitterFraction: 0.2,
	}

	// Deterministic mid-point jitter (randF=0.5 -> delta=0): 100ms, 200ms, 400ms, 500ms (capped).
	mid := func() float64 { return 0.5 }
	want := []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond, 500 * time.Millisecond}
	for i, w := range want {
		got := BackoffDelay(i+1, pol, mid)
		if got != w {
			t.Fatalf("attempt %d delay=%v want %v", i+1, got, w)
		}
	}

	// Min and max jitter bounds stay strictly within [80ms, 120ms] for attempt=1 and never exceed MaxBackoffMS.
	dMin := BackoffDelay(1, pol, func() float64 { return 0.0 })
	dMax := BackoffDelay(1, pol, func() float64 { return 0.9999 })
	if dMin != 80*time.Millisecond || dMax > 120*time.Millisecond {
		t.Fatalf("attempt 1 jitter bounds unexpected: min=%v max=%v", dMin, dMax)
	}
	dCap := BackoffDelay(4, pol, func() float64 { return 0.9999 })
	if dCap > 500*time.Millisecond {
		t.Fatalf("jitter exceeded MaxBackoffMS: %v", dCap)
	}

	// Deadline budget guard: if remaining deadline <= delay, SleepWithBudget returns false immediately without waiting!
	fakeNow := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	ctxShort, cancelShort := context.WithDeadline(context.Background(), fakeNow.Add(50*time.Millisecond))
	defer cancelShort()
	if ok := SleepWithBudget(ctxShort, 100*time.Millisecond, func() time.Time { return fakeNow }); ok {
		t.Fatal("expected SleepWithBudget to return false when remaining deadline < delay")
	}

	// Canceled context returns false immediately.
	ctxCanceled, cancel := context.WithCancel(context.Background())
	cancel()
	if ok := SleepWithBudget(ctxCanceled, 5*time.Millisecond, time.Now); ok {
		t.Fatal("expected SleepWithBudget to return false on canceled context")
	}

	// Sufficient budget completes cleanly.
	ctxOK, cancelOK := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancelOK()
	if ok := SleepWithBudget(ctxOK, 5*time.Millisecond, time.Now); !ok {
		t.Fatal("expected SleepWithBudget to succeed with sufficient deadline budget")
	}
}
