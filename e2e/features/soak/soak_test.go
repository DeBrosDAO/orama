//go:build e2e_fleet

package soak

import (
	"context"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/features/internal/realistic"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

const (
	// EnvSoakMinutes is how long the traffic runs; the stage's package
	// timeout (stages.yaml, 120m) must leave room for setup and recovery.
	EnvSoakMinutes     = "E2E_SOAK_MINUTES"
	defaultSoakMinutes = 30
	feature            = "soak"
)

func soakDuration(t *testing.T) time.Duration {
	t.Helper()
	raw := os.Getenv(EnvSoakMinutes)
	if raw == "" {
		return defaultSoakMinutes * time.Minute
	}
	m, err := strconv.Atoi(raw)
	if err != nil || m <= 0 {
		t.Fatalf("%s=%q is not a positive number of minutes", EnvSoakMinutes, raw)
	}
	return time.Duration(m) * time.Minute
}

// TestSoak_mixedTrafficUnderScheduledChaos runs the soak described in
// feature.yaml and records its numbers in <artifacts>/soak/slo.json.
func TestSoak_mixedTrafficUnderScheduledChaos(t *testing.T) {
	f := harness.Fleet(t)
	d := soakDuration(t)
	infra.RequireHealthy(t)
	w := setupWorkload(t)
	units := watched(f, w.tn.N.Name)
	before := readAll(t, f, units)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	fd := &feed{at: map[int64]time.Time{}}
	var wg sync.WaitGroup
	subs := startSubscribers(ctx, w, &wg)
	loads := startLoads(ctx, w, fd)
	ws := &windows{}
	killed := runSchedule(t, f, w, ws, d)
	end := time.Now()
	samples := map[string][]realistic.Sample{}
	for name, l := range loads {
		samples[name] = l.Stop()
	}
	cancel()
	wg.Wait()
	infra.WaitConverged(t, len(f.State.Nodes), infra.ConvergeBudget, "the cluster at the end of the soak")
	after := readAll(t, f, units)
	traffic := checkTraffic(t, samples, ws)
	sockets := checkSockets(t, subs, fd, ws, end)
	checkFootprint(t, before, after, killed)
	realistic.WriteJSON(t, f, feature, "slo.json", map[string]any{
		"duration": d.String(), "windows": ws.list, "traffic": traffic, "sockets": sockets,
		"footprint_before": before, "footprint_after": after, "killed": killed,
	})
}

// startLoads starts every op of the mixed traffic and the feed.
func startLoads(ctx context.Context, w *workload, fd *feed) map[string]*realistic.Load {
	out := map[string]*realistic.Load{}
	for _, o := range append(w.ops(), op{"publish", 1, time.Second, fd.publish(w)}) {
		out[o.name] = realistic.StartLoad(ctx, o.workers, o.interval, o.call)
	}
	return out
}

// runSchedule fires the faults spread over d, holding the cluster
// converged between them, and returns how often each daemon was killed.
func runSchedule(t *testing.T, f *fleet.Fleet, w *workload, ws *windows, d time.Duration) map[string]int {
	t.Helper()
	events := schedule(f, w)
	gap := d / time.Duration(len(events)+1)
	start := time.Now()
	killed := map[string]int{}
	for i, ev := range events {
		quiet(t, time.Until(start.Add(time.Duration(i+1)*gap)))
		fire(t, ws, ev)
		for _, k := range ev.kills {
			killed[k]++
		}
	}
	quiet(t, time.Until(start.Add(d)))
	return killed
}
