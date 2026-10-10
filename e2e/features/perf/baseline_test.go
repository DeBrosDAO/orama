//go:build e2e_fleet

package perf

import (
	"fmt"
	"math"
	"net/http"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/realistic"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

const (
	feature = "perf"
	// EnvBaselineFile names a previous run's perf/baseline.json to compare with.
	EnvBaselineFile = "E2E_BASELINE_FILE"
	// EnvRegressionPct is how much worse (percent) a metric may be than the
	// previous baseline before it fails.
	EnvRegressionPct     = "E2E_PERF_REGRESSION_PCT"
	defaultRegressionPct = 25
	// regressionFloorMS: a p95 that moved by less than this is jitter of the
	// path from the runner, not a regression, whatever the percentage.
	regressionFloorMS = 25
	baselineFile      = "baseline.json"
)

// Baseline is what a run records and a later run compares against.
type Baseline struct {
	Release    string                       `json:"release"`
	RunID      string                       `json:"run_id"`
	RecordedAt time.Time                    `json:"recorded_at"`
	Metrics    map[string]realistic.Summary `json:"metrics"`
}

// recorded is this run's baseline, filled as the tests measure.
var recorded = struct {
	sync.Mutex
	b    Baseline
	once sync.Once
}{}

// release reads the version the fleet runs, once.
func release(t testing.TB, f *fleet.Fleet) string {
	t.Helper()
	recorded.once.Do(func() {
		var v struct {
			Version string `json:"version"`
		}
		if err := harness.GW(t).MustSend(t, gw.Req{Path: "/v1/version"}).Expect(t, http.StatusOK).Decode(&v); err != nil {
			t.Fatal(err)
		}
		recorded.b = Baseline{Release: v.Version, RunID: f.State.RunID, RecordedAt: time.Now().UTC(), Metrics: map[string]realistic.Summary{}}
	})
	return recorded.b.Release
}

// record keeps sum in this run's baseline (per metric and in baseline.json),
// fails on any error or a p95 over bound, and compares with the previous
// baseline when one is given.
func record(t testing.TB, f *fleet.Fleet, sum realistic.Summary, bound time.Duration) {
	t.Helper()
	release(t, f)
	recorded.Lock()
	recorded.b.Metrics[sum.Name] = sum
	snapshot := recorded.b
	recorded.Unlock()
	realistic.WriteJSON(t, f, feature, sum.Name+".json", sum)
	realistic.WriteJSON(t, f, feature, baselineFile, snapshot)
	t.Log(sum.String())
	if sum.Errors > 0 {
		t.Errorf("%d of %d operations failed: %s (first: %s)", sum.Errors, sum.Count, sum, sum.FirstError)
	}
	if limit := float64(bound.Milliseconds()); sum.P95MS > limit {
		t.Errorf("p95 over its bound of %s: %s", bound, sum)
	}
	compare(t, sum)
}

// compare fails when sum regressed against the previous baseline.
func compare(t testing.TB, sum realistic.Summary) {
	t.Helper()
	path := os.Getenv(EnvBaselineFile)
	if path == "" {
		return
	}
	var prev Baseline
	if err := realistic.ReadJSON(path, &prev); err != nil {
		t.Fatalf("%s: %v", EnvBaselineFile, err)
	}
	old, ok := prev.Metrics[sum.Name]
	if !ok {
		t.Logf("%s is new: the previous baseline (%s) has no such metric", sum.Name, prev.Release)
		return
	}
	pct := regressionPct(t)
	allowed := old.P95MS * (1 + pct/100)
	if sum.P95MS > allowed && sum.P95MS-old.P95MS >= regressionFloorMS {
		t.Errorf("%s p95 regressed %.0f%% against %s (%.1fms -> %.1fms, allowed %.0f%%)",
			sum.Name, 100*(sum.P95MS/old.P95MS-1), prev.Release, old.P95MS, sum.P95MS, pct)
	}
	if old.Throughput > 0 && sum.Throughput < old.Throughput*(1-pct/100) {
		t.Errorf("%s throughput regressed against %s (%.1f/s -> %.1f/s, allowed %.0f%%)",
			sum.Name, prev.Release, old.Throughput, sum.Throughput, pct)
	}
}

func regressionPct(t testing.TB) float64 {
	t.Helper()
	raw := os.Getenv(EnvRegressionPct)
	if raw == "" {
		return defaultRegressionPct
	}
	pct, err := strconv.ParseFloat(raw, 64)
	if err != nil || pct <= 0 || math.IsInf(pct, 0) {
		t.Fatalf("%s=%q is not a positive percentage", EnvRegressionPct, raw)
	}
	return pct
}

// once measures a single timed operation (a deploy, a provision) as a metric.
func once(name string, start time.Time, err error) realistic.Summary {
	return realistic.Summarize(name, []realistic.Sample{{At: start, Duration: time.Since(start), Err: err}}, nil)
}

// check turns an answer other than 200 into an operation error.
func check(r *gw.Response, err error) error {
	if err != nil {
		return err
	}
	if r.Status != http.StatusOK {
		return fmt.Errorf("HTTP %d %.160s", r.Status, r.Body)
	}
	return nil
}
