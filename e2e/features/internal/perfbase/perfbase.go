//go:build e2e_fleet

// Package perfbase is the baseline and comparison code the performance
// packages (perf for the gateway and data plane, chain-perf for the chain)
// share: a run records every metric as a realistic.Summary, writes the
// metrics so far to <artifact dir>/<feature>/baseline.json, fails a metric
// whose error count or p95 is over its bound, and, when a previous run's
// baseline file is named, fails a p95 or throughput more than
// E2E_PERF_REGRESSION_PCT percent worse (default 25).
package perfbase

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
	// EnvRegressionPct is how much worse (percent) a metric may be than the
	// previous baseline before it fails. All performance packages read it.
	EnvRegressionPct = "E2E_PERF_REGRESSION_PCT"
	// DefaultRegressionPct is used when EnvRegressionPct is unset.
	DefaultRegressionPct = 25
	// RegressionFloorMS: a p95 that moved by less than this is jitter of the
	// path from the runner, not a regression, whatever the percentage.
	RegressionFloorMS = 25
	// FileName is the baseline file inside the feature's artifact directory.
	FileName = "baseline.json"
)

// Baseline is what a run records and a later run compares against.
type Baseline struct {
	Release    string                       `json:"release"`
	RunID      string                       `json:"run_id"`
	RecordedAt time.Time                    `json:"recorded_at"`
	Metrics    map[string]realistic.Summary `json:"metrics"`
}

// Recorder keeps one feature's baseline while its tests measure.
type Recorder struct {
	feature string
	// envFile names the environment variable holding the previous baseline file.
	envFile string
	// release reads the version the fleet runs.
	release func(testing.TB, *fleet.Fleet) string

	mu   sync.Mutex
	once sync.Once
	b    Baseline
}

// New returns a Recorder for feature. envFile is the variable naming the
// previous baseline to compare with; release reads the version under test.
func New(feature, envFile string, release func(testing.TB, *fleet.Fleet) string) *Recorder {
	return &Recorder{feature: feature, envFile: envFile, release: release}
}

// GatewayRelease reads the version the fleet's gateway reports, the release
// under test.
func GatewayRelease(t testing.TB, _ *fleet.Fleet) string {
	t.Helper()
	var v struct {
		Version string `json:"version"`
	}
	if err := harness.GW(t).MustSend(t, gw.Req{Path: "/v1/version"}).Expect(t, http.StatusOK).Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v.Version
}

// Release is the version under test, read once.
func (r *Recorder) Release(t testing.TB, f *fleet.Fleet) string {
	t.Helper()
	r.once.Do(func() {
		r.b = Baseline{Release: r.release(t, f), RunID: f.State.RunID, RecordedAt: time.Now().UTC(), Metrics: map[string]realistic.Summary{}}
	})
	return r.b.Release
}

// Record keeps sum in this run's baseline (per metric and in baseline.json),
// fails on any error or a p95 over bound, and compares with the previous
// baseline when one is given.
func (r *Recorder) Record(t testing.TB, f *fleet.Fleet, sum realistic.Summary, bound time.Duration) {
	t.Helper()
	r.Release(t, f)
	r.mu.Lock()
	r.b.Metrics[sum.Name] = sum
	snapshot := r.b
	r.mu.Unlock()
	realistic.WriteJSON(t, f, r.feature, sum.Name+".json", sum)
	realistic.WriteJSON(t, f, r.feature, FileName, snapshot)
	t.Log(sum.String())
	if sum.Errors > 0 {
		t.Errorf("%d of %d operations failed: %s (first: %s)", sum.Errors, sum.Count, sum, sum.FirstError)
	}
	if limit := float64(bound.Milliseconds()); sum.P95MS > limit {
		t.Errorf("p95 over its bound of %s: %s", bound, sum)
	}
	r.compare(t, sum)
}

// compare fails when sum regressed against the previous baseline.
func (r *Recorder) compare(t testing.TB, sum realistic.Summary) {
	t.Helper()
	path := os.Getenv(r.envFile)
	if path == "" {
		return
	}
	var prev Baseline
	if err := realistic.ReadJSON(path, &prev); err != nil {
		t.Fatalf("%s: %v", r.envFile, err)
	}
	old, ok := prev.Metrics[sum.Name]
	if !ok {
		t.Logf("%s is new: the previous baseline (%s) has no such metric", sum.Name, prev.Release)
		return
	}
	pct := RegressionPct(t)
	for _, msg := range Regressions(old, sum, pct) {
		t.Errorf("%s (previous baseline %s)", msg, prev.Release)
	}
}

// Regressions names how cur is worse than old by more than pct percent: a
// p95 that grew by that much and by at least RegressionFloorMS, or a
// throughput that fell by that much. An empty result is no regression.
func Regressions(old, cur realistic.Summary, pct float64) []string {
	var out []string
	allowed := old.P95MS * (1 + pct/100)
	if cur.P95MS > allowed && cur.P95MS-old.P95MS >= RegressionFloorMS {
		out = append(out, fmt.Sprintf("%s p95 regressed %.0f%% (%.1fms -> %.1fms, allowed %.0f%%)",
			cur.Name, 100*(cur.P95MS/old.P95MS-1), old.P95MS, cur.P95MS, pct))
	}
	if old.Throughput > 0 && cur.Throughput < old.Throughput*(1-pct/100) {
		out = append(out, fmt.Sprintf("%s throughput regressed (%.1f/s -> %.1f/s, allowed %.0f%%)",
			cur.Name, old.Throughput, cur.Throughput, pct))
	}
	return out
}

// RegressionPct is the allowed regression, EnvRegressionPct or the default.
func RegressionPct(t testing.TB) float64 {
	t.Helper()
	raw := os.Getenv(EnvRegressionPct)
	if raw == "" {
		return DefaultRegressionPct
	}
	pct, err := strconv.ParseFloat(raw, 64)
	if err != nil || pct <= 0 || math.IsInf(pct, 0) {
		t.Fatalf("%s=%q is not a positive percentage", EnvRegressionPct, raw)
	}
	return pct
}
