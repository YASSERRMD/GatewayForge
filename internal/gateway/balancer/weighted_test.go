package balancer

import (
	"errors"
	"sync"
	"testing"

	"github.com/YASSERRMD/GatewayForge/internal/shared/snapshot"
)

func TestWeightedRoundRobinDistributionAndEdgeCases(t *testing.T) {
	wrr := NewWeightedRoundRobin()

	t.Run("exact smooth 3:1 weight distribution", func(t *testing.T) {
		pool := snapshot.UpstreamPool{
			ID: "pool-dist",
			Targets: []snapshot.UpstreamTarget{
				{ID: "heavy", URL: "http://t1:8089", Weight: 300, Enabled: true},
				{ID: "light", URL: "http://t2:8089", Weight: 100, Enabled: true},
				{ID: "disabled", URL: "http://t3:8089", Weight: 500, Enabled: false},
			},
		}
		counts := map[string]int{}
		for i := 0; i < 400; i++ {
			chosen, err := wrr.Pick(pool, nil)
			if err != nil {
				t.Fatalf("Pick %d failed: %v", i, err)
			}
			counts[chosen.ID]++
		}
		if counts["heavy"] != 300 || counts["light"] != 100 || counts["disabled"] != 0 {
			t.Fatalf("unexpected weight distribution: %+v", counts)
		}
	})

	t.Run("returns ErrNoAvailableTargets when empty or all disabled", func(t *testing.T) {
		empty := snapshot.UpstreamPool{ID: "empty"}
		if _, err := wrr.Pick(empty, nil); !errors.Is(err, ErrNoAvailableTargets) {
			t.Fatalf("expected ErrNoAvailableTargets for empty pool, got %v", err)
		}

		allDisabled := snapshot.UpstreamPool{
			ID: "off",
			Targets: []snapshot.UpstreamTarget{
				{ID: "t1", URL: "http://t1:8089", Weight: 100, Enabled: false},
			},
		}
		if _, err := wrr.Pick(allDisabled, nil); !errors.Is(err, ErrNoAvailableTargets) {
			t.Fatalf("expected ErrNoAvailableTargets for all-disabled pool, got %v", err)
		}
	})

	t.Run("skips unhealthy targets and stays race-safe concurrently", func(t *testing.T) {
		pool := snapshot.UpstreamPool{
			ID: "pool-race",
			Targets: []snapshot.UpstreamTarget{
				{ID: "t1", URL: "http://t1:8089", Weight: 2, Enabled: true},
				{ID: "t2", URL: "http://t2:8089", Weight: 1, Enabled: true},
				{ID: "t3", URL: "http://t3:8089", Weight: 10, Enabled: true},
			},
		}
		isHealthy := func(id string) bool { return id != "t3" }

		var wg sync.WaitGroup
		for g := 0; g < 8; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < 100; i++ {
					chosen, err := wrr.Pick(pool, isHealthy)
					if err != nil {
						t.Errorf("Pick failed: %v", err)
						return
					}
					if chosen.ID == "t3" {
						t.Errorf("unhealthy target t3 was selected")
					}
				}
			}()
		}
		wg.Wait()
	})
}
