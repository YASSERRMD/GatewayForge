package telemetry

import (
	"testing"
	"time"
)

func TestParseRangeBucketSelectionAndPercentiles(t *testing.T) {
	cases := []struct {
		in      string
		wantLbl string
		wantWin time.Duration
		wantMin int
	}{
		{"15m", "15m", 15 * time.Minute, 1},
		{"1h", "1h", time.Hour, 5},
		{"6h", "6h", 6 * time.Hour, 15},
		{"24h", "24h", 24 * time.Hour, 60},
		{"invalid", "1h", time.Hour, 5},
	}
	for _, tc := range cases {
		lbl, win, step := ParseRange(tc.in)
		if lbl != tc.wantLbl || win != tc.wantWin || step != tc.wantMin {
			t.Fatalf("ParseRange(%q) = (%s,%v,%d) want (%s,%v,%d)", tc.in, lbl, win, step, tc.wantLbl, tc.wantWin, tc.wantMin)
		}
	}

	// Empty counts -> 0ms
	if got := estimatePercentileMS([]int64{0, 0, 0, 0, 0, 0, 0, 0}, 0.95); got != 0 {
		t.Fatalf("empty percentile=%v want 0", got)
	}
	// 90 fast (<=10ms) + 10 slow (<=250ms): p50=10ms, p95=250ms
	counts := []int64{90, 0, 0, 10, 0, 0, 0, 0}
	if p50 := estimatePercentileMS(counts, 0.50); p50 != 10 {
		t.Fatalf("p50=%v want 10", p50)
	}
	if p95 := estimatePercentileMS(counts, 0.95); p95 != 250 {
		t.Fatalf("p95=%v want 250", p95)
	}
}
