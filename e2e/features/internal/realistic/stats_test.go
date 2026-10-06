//go:build e2e_fleet

package realistic

import (
	"errors"
	"testing"
	"time"
)

func TestSummarize_keepsTheSlowestSuccessesWithTheirStart(t *testing.T) {
	t0 := time.Date(2026, 10, 4, 15, 0, 0, 0, time.UTC)
	var samples []Sample
	for i := range 15 {
		samples = append(samples, Sample{At: t0.Add(time.Duration(i) * time.Second), Duration: time.Duration(i+1) * time.Millisecond})
	}
	samples = append(samples, Sample{At: t0, Duration: time.Hour, Err: errors.New("failed")})
	got := Summarize("op", samples, nil).Slowest
	if len(got) != slowestKept {
		t.Fatalf("kept %d, want %d", len(got), slowestKept)
	}
	if got[0].MS != 15 || !got[0].At.Equal(t0.Add(14*time.Second)) {
		t.Fatalf("slowest is %+v, want the 15ms one started at +14s", got[0])
	}
	if got[slowestKept-1].MS != 6 {
		t.Fatalf("last kept is %vms, want 6ms", got[slowestKept-1].MS)
	}
}

func TestSummarize_noSamplesKeepsNoSlowest(t *testing.T) {
	if got := Summarize("op", nil, nil).Slowest; len(got) != 0 {
		t.Fatalf("slowest %v from no samples", got)
	}
}
