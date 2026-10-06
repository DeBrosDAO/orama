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
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

const (
	// EnvSoakMinutes is how long the traffic runs; it is capped at what the
	// stage budget leaves after setup, the faults and teardown (soakDuration).
	EnvSoakMinutes     = "E2E_SOAK_MINUTES"
	defaultSoakMinutes = 30
	feature            = "soak"
	// setupBudget bounds the workload's setup: its namespace becoming ready
	// and the static site deployed.
	setupBudget = 2 * ns.ReadyBudget
	// eventWorst is the most one scheduled fault takes beyond its planned
	// gap: the fault (a restart, or a hold), the convergence after it and
	// its cleanup restoring the node.
	eventWorst = restartBudget + faultHold + infra.ConvergeBudget + fleet.CleanupBudget
	// teardownBudget covers the final convergence and the namespace's teardown.
	teardownBudget = infra.ConvergeBudget + ns.TeardownBudget
)

// soakDuration is E2E_SOAK_MINUTES (default 30), refused when it does not fit
// in the stage budget with the setup, every fault at its worst and the
// teardown: past the budget the runner interrupts the soak mid-fault.
func soakDuration(t *testing.T, events int) time.Duration {
	t.Helper()
	d := defaultSoakMinutes * time.Minute
	if raw := os.Getenv(EnvSoakMinutes); raw != "" {
		m, err := strconv.Atoi(raw)
		if err != nil || m <= 0 {
			t.Fatalf("%s=%q is not a positive number of minutes", EnvSoakMinutes, raw)
		}
		d = time.Duration(m) * time.Minute
	}
	overhead := setupBudget + time.Duration(events)*eventWorst + teardownBudget
	if left, ok := realistic.StageRemaining(t); ok && d > left-overhead {
		t.Fatalf("%s=%d does not fit: the stage budget leaves %v, and setup, %d faults at their worst and teardown take %v; "+
			"at most %d minutes (or raise stage 11's timeout in e2e/stages/stages.yaml)",
			EnvSoakMinutes, int(d/time.Minute), left.Round(time.Minute), events, overhead, int((left-overhead)/time.Minute))
	}
	return d
}

// TestSoak_mixedTrafficUnderScheduledChaos runs the soak described in
// feature.yaml and records its numbers in <artifacts>/soak/slo.json.
func TestSoak_mixedTrafficUnderScheduledChaos(t *testing.T) {
	f := harness.Fleet(t)
	// schedule's closures reference the workload only when they run: here
	// only the number of faults is read.
	d := soakDuration(t, len(schedule(f, nil)))
	infra.RequireHealthy(t)
	w := setupWorkload(t)
	units := watched(t, f, w.tn.N.Name)
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
		if !realistic.FitsStageBudget(t, eventWorst+teardownBudget) {
			t.Errorf("not firing %q or the %d faults after it: the stage budget no longer fits one at its worst (%v) and the teardown; "+
				"raise stage 11's timeout or lower %s", ev.name, len(events)-i-1, eventWorst, EnvSoakMinutes)
			break
		}
		fire(t, ws, ev)
		for _, k := range ev.kills {
			killed[k]++
		}
	}
	quiet(t, time.Until(start.Add(d)))
	return killed
}
