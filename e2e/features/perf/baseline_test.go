//go:build e2e_fleet

package perf

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/perfbase"
	"github.com/DeBrosOfficial/network/e2e/features/internal/realistic"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

const (
	feature = "perf"
	// EnvBaselineFile names a previous run's perf/baseline.json to compare with.
	EnvBaselineFile = "E2E_BASELINE_FILE"
)

// recorder is this run's baseline, filled as the tests measure; the recording
// and the comparison with the previous baseline are features/internal/perfbase.
var recorder = perfbase.New(feature, EnvBaselineFile, perfbase.GatewayRelease)

// record keeps sum in this run's baseline (per metric and in baseline.json),
// fails on any error or a p95 over bound, and compares with the previous
// baseline when one is given.
func record(t testing.TB, f *fleet.Fleet, sum realistic.Summary, bound time.Duration) {
	t.Helper()
	recorder.Record(t, f, sum, bound)
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
