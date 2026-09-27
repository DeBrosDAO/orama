package report

import "testing"

// A unit that never reaches active reports ActiveEnterTimestamp as "n/a", so
// ActiveSinceSec stays 0. That is the worst kind of crash loop, not the absence
// of one — and since the supervised units carry StartLimitIntervalSec=0 nothing
// parks them in `failed` any more, so this counter is the only signal left.
func TestRestartLoopRisk(t *testing.T) {
	tests := []struct {
		name           string
		nRestarts      int
		activeSinceSec int64
		want           bool
	}{
		{"healthy long-running unit", 0, 86400, false},
		{"a few restarts but stable since", 5, 3600, false},
		{"restarting and only briefly up", 7, 12, true},
		{"never reached active at all", 7, 0, true},
		{"never active but not yet suspicious", 2, 0, false},
		{"exactly at the threshold is not a loop", 3, 0, false},
		{"one past the threshold is", 4, 0, true},
		{"up for exactly the recovery window", 9, 300, false},
		{"one second short of it", 9, 299, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := restartLoopRisk(tc.nRestarts, tc.activeSinceSec); got != tc.want {
				t.Fatalf("restartLoopRisk(%d, %d) = %v, want %v", tc.nRestarts, tc.activeSinceSec, got, tc.want)
			}
		})
	}
}
