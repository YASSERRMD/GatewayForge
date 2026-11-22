package telemetry

import (
	"testing"
	"time"
)

func TestHeartbeatClassificationAndStalenessThresholds(t *testing.T) {
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)

	// 5s ago -> healthy
	st, age := ClassifyHeartbeat(now, now.Add(-5*time.Second), 30*time.Second, 60*time.Second)
	if st != "healthy" || age != 5 {
		t.Fatalf("5s ago: status=%q age=%d want healthy/5", st, age)
	}

	// 40s ago (missed a couple beats, not yet offline!) -> stale
	st, age = ClassifyHeartbeat(now, now.Add(-40*time.Second), 30*time.Second, 60*time.Second)
	if st != "stale" || age != 40 {
		t.Fatalf("40s ago: status=%q age=%d want stale/40", st, age)
	}

	// 75s ago -> offline
	st, age = ClassifyHeartbeat(now, now.Add(-75*time.Second), 30*time.Second, 60*time.Second)
	if st != "offline" || age != 75 {
		t.Fatalf("75s ago: status=%q age=%d want offline/75", st, age)
	}
}
