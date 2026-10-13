package runner

import (
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/profile"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/report"
)

func TestSendEnd(t *testing.T) {
	start := time.Unix(1_700_000_000, 0)
	open := profile.Step{Mode: report.ModeOpen, Rate: 10, Duration: 10 * time.Second}
	closed := profile.Step{Mode: report.ModeClosed, Window: 8, Duration: 10 * time.Second}
	cases := []struct {
		name     string
		spec     profile.Step
		offered  int
		driveEnd time.Duration
		want     time.Duration
	}{
		{"open, everything offered", open, 100, 9900 * time.Millisecond, 10 * time.Second},
		{"open, the type ran out at half", open, 50, 4900 * time.Millisecond, 5 * time.Second},
		{"open, nothing offered", open, 0, 0, 0},
		{"open, more than the duration holds", open, 150, 10 * time.Second, 10 * time.Second},
		{"closed, ran its whole time", closed, 90, 10 * time.Second, 10 * time.Second},
		{"closed, out of signed transactions at 4s", closed, 40, 4 * time.Second, 4 * time.Second},
		{"closed, the last offer ran past the duration", closed, 90, 10500 * time.Millisecond, 10 * time.Second},
	}
	for _, c := range cases {
		got := sendEnd(c.spec, start, c.offered, start.Add(c.driveEnd))
		if got.Sub(start) != c.want {
			t.Errorf("%s: the window is %s, want %s", c.name, got.Sub(start), c.want)
		}
	}
	zero := profile.Step{Mode: report.ModeOpen, Duration: time.Second}
	if got := sendEnd(zero, start, 5, start); got.Sub(start) != time.Second {
		t.Errorf("a step with no rate must keep its nominal window, got %s", got.Sub(start))
	}
}
