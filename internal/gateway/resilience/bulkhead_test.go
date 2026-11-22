package resilience

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestBulkheadSaturationAndRecovery(t *testing.T) {
	reg := NewBulkheadRegistry(2)

	// Saturate limit=2 concurrently using a barrier channel.
	hold := make(chan struct{})
	var admitted, rejected atomic.Int32
	var wg sync.WaitGroup

	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rel, ok := reg.Acquire("rt-1", 2)
			if !ok {
				rejected.Add(1)
				return
			}
			admitted.Add(1)
			<-hold
			rel()
		}()
	}

	// Spin briefly until all 6 goroutines have attempted Acquire.
	for admitted.Load()+rejected.Load() < 6 {
		// yield
	}
	if admitted.Load() != 2 || rejected.Load() != 4 {
		t.Fatalf("expected 2 admitted / 4 rejected, got admitted=%d rejected=%d", admitted.Load(), rejected.Load())
	}
	if reg.InFlight("rt-1") != 2 {
		t.Fatalf("inFlight=%d want 2", reg.InFlight("rt-1"))
	}

	// Release in-flight operations -> immediate recovery back to 0 in-flight.
	close(hold)
	wg.Wait()
	if reg.InFlight("rt-1") != 0 {
		t.Fatalf("after release inFlight=%d want 0", reg.InFlight("rt-1"))
	}

	rel, ok := reg.Acquire("rt-1", 2)
	if !ok {
		t.Fatal("expected bulkhead to recover immediately after in-flight requests finished")
	}
	rel()
	// Double release is idempotent and must not underflow.
	rel()
	if reg.InFlight("rt-1") != 0 {
		t.Fatalf("double release corrupted inFlight=%d", reg.InFlight("rt-1"))
	}
}
