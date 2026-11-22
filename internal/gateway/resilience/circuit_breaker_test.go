package resilience

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/YASSERRMD/GatewayForge/internal/shared/policy"
)

func TestCircuitBreakerStateMachineAndHalfOpenBounds(t *testing.T) {
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	reg := NewCircuitBreakerRegistry(func() time.Time { return now })
	pol := policy.CircuitBreakerPolicy{
		FailureThreshold:    3,
		SuccessThreshold:    2,
		OpenDurationMS:      5000,
		HalfOpenMaxInFlight: 1,
	}

	// 1. Business 4xx does not open breaker.
	for i := 0; i < 5; i++ {
		rec, ok := reg.Allow("pool-1", pol)
		if !ok {
			t.Fatal("closed breaker should allow")
		}
		rec(http.StatusNotFound, nil)
	}
	if reg.State("pool-1") != CircuitClosed {
		t.Fatalf("expected closed after 404s, got %s", reg.State("pool-1"))
	}

	// 2. 3 consecutive upstream failures trip breaker to OPEN.
	for i := 0; i < 3; i++ {
		rec, ok := reg.Allow("pool-1", pol)
		if !ok {
			t.Fatalf("attempt %d should be allowed before tripping", i+1)
		}
		rec(0, errors.New("dial timeout"))
	}
	if reg.State("pool-1") != CircuitOpen {
		t.Fatalf("expected open after 3 failures, got %s", reg.State("pool-1"))
	}
	if _, ok := reg.Allow("pool-1", pol); ok {
		t.Fatal("open breaker must reject traffic before cooldown")
	}

	// 3. Advance 5s -> transitions to HALF_OPEN with max 1 concurrent trial.
	now = now.Add(5 * time.Second)
	rec1, ok1 := reg.Allow("pool-1", pol)
	if !ok1 {
		t.Fatal("half-open should admit first trial request")
	}
	if _, ok2 := reg.Allow("pool-1", pol); ok2 {
		t.Fatal("half-open must reject concurrent requests beyond HalfOpenMaxInFlight=1")
	}
	// If trial fails, immediately re-opens for another 5s!
	rec1(http.StatusBadGateway, nil)
	if reg.State("pool-1") != CircuitOpen {
		t.Fatalf("half-open failure must re-open breaker, got %s", reg.State("pool-1"))
	}

	// 4. Advance another 5s -> 2 consecutive successes close the breaker!
	now = now.Add(5 * time.Second)
	rA, okA := reg.Allow("pool-1", pol)
	if !okA {
		t.Fatal("expected half-open probe A")
	}
	rA(http.StatusOK, nil)
	if reg.State("pool-1") != CircuitHalfOpen {
		t.Fatalf("expected half_open after 1/2 successes, got %s", reg.State("pool-1"))
	}

	rB, okB := reg.Allow("pool-1", pol)
	if !okB {
		t.Fatal("expected half-open probe B")
	}
	rB(http.StatusOK, nil)
	if reg.State("pool-1") != CircuitClosed {
		t.Fatalf("expected closed after 2/2 successes, got %s", reg.State("pool-1"))
	}
}
