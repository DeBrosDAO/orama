//go:build e2e_fleet

package perfbase

import (
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/realistic"
)

func summary(p95, throughput float64) realistic.Summary {
	return realistic.Summary{Name: "m", P95MS: p95, Throughput: throughput}
}

func TestRegressions_p95OverThePercentageAndTheFloor(t *testing.T) {
	if got := Regressions(summary(100, 0), summary(200, 0), DefaultRegressionPct); len(got) != 1 {
		t.Fatalf("a doubled p95 reported %v", got)
	}
	if got := Regressions(summary(100, 0), summary(124, 0), DefaultRegressionPct); len(got) != 0 {
		t.Fatalf("a 24%% p95 rise is inside the 25%% allowance: %v", got)
	}
	if got := Regressions(summary(100, 0), summary(130, 0), 50); len(got) != 0 {
		t.Fatalf("a 30%% rise is inside a 50%% allowance: %v", got)
	}
}

func TestRegressions_aRiseUnderTheFloorIsJitter(t *testing.T) {
	// 1ms -> 10ms is +900% but 9ms, under RegressionFloorMS.
	if got := Regressions(summary(1, 0), summary(10, 0), DefaultRegressionPct); len(got) != 0 {
		t.Fatalf("a 9ms rise was reported: %v", got)
	}
}

func TestRegressions_throughputFall(t *testing.T) {
	if got := Regressions(summary(100, 100), summary(100, 70), DefaultRegressionPct); len(got) != 1 {
		t.Fatalf("a 30%% throughput fall reported %v", got)
	}
	if got := Regressions(summary(100, 100), summary(100, 80), DefaultRegressionPct); len(got) != 0 {
		t.Fatalf("a 20%% throughput fall reported %v", got)
	}
}

func TestRegressions_aBaselineWithoutThroughputHasNoThroughputRegression(t *testing.T) {
	if got := Regressions(summary(100, 0), summary(100, 0), DefaultRegressionPct); len(got) != 0 {
		t.Fatalf("identical metrics reported %v", got)
	}
}

func TestRegressions_bothAtOnce(t *testing.T) {
	if got := Regressions(summary(100, 100), summary(300, 10), DefaultRegressionPct); len(got) != 2 {
		t.Fatalf("a slower and lower-throughput run reported %v", got)
	}
}

func TestRegressionPct_environment(t *testing.T) {
	t.Setenv(EnvRegressionPct, "")
	if got := RegressionPct(t); got != DefaultRegressionPct {
		t.Fatalf("default = %v", got)
	}
	t.Setenv(EnvRegressionPct, "40")
	if got := RegressionPct(t); got != 40 {
		t.Fatalf("40 = %v", got)
	}
}
