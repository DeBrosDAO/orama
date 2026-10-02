//go:build e2e_fleet

package soak

import (
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/edge"
	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/features/internal/realistic"
	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/monitor"
)

const (
	faultHold     = time.Minute      // how long a partition, a skew or a stop lasts
	clockSkew     = 10 * time.Second // well past monitoring's 5s skew warning
	observeEvery  = 30 * time.Second
	restartBudget = 2 * time.Minute
	// maxAppliedLag is monitoring's warning threshold (docs/MONITORING.md).
	maxAppliedLag = 100
)

// window is the span a chaos event disturbed: from injection until the
// cluster had converged again.
type window struct {
	Name  string    `json:"name"`
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// windows are the injected windows; traffic in them is not held to the SLOs.
type windows struct {
	mu   sync.Mutex
	list []window
}

func (ws *windows) add(w window) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	ws.list = append(ws.list, w)
}

// covers reports whether at falls in a window, widened by deliverGrace.
func (ws *windows) covers(at time.Time) bool {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	for _, w := range ws.list {
		if !at.Before(w.Start.Add(-deliverGrace)) && !at.After(w.End.Add(deliverGrace)) {
			return true
		}
	}
	return false
}

// event is one fault; its t.Cleanup restores what it broke.
type event struct {
	name string
	run  func(t *testing.T)
	// kills are the node/unit daemons the event SIGKILLs: each explains one
	// restart and a reset footprint.
	kills []string
}

// schedule is the soak's faults, in the order they fire.
func schedule(f *fleet.Fleet, w *workload) []event {
	n := f.State.Nodes
	pick := func(i int) fleet.Node { return n[i%len(n)] }
	kill := func(node fleet.Node, what, unit string) event {
		return event{name: "kill -9 " + what + " on " + node.Name, run: func(t *testing.T) { killAndWait(t, f, node, unit) }, kills: []string{node.Name + "/" + unit}}
	}
	return []event{
		kill(pick(1), "the cluster gateway", edge.IndexGatewayUnit),
		{name: "partition " + pick(2).Name, run: func(t *testing.T) {
			for _, o := range n {
				if o.Name != pick(2).Name {
					f.IPTablesBlock(t, pick(2), o)
				}
			}
			holdServing(t, w, others(n, pick(2)))
		}},
		{name: "clock skew on " + pick(0).Name, run: func(t *testing.T) { f.ClockSkew(t, pick(0), clockSkew); holdServing(t, w, n) }},
		{name: "restart the namespace gateway on a member", run: func(t *testing.T) {
			victim := tenancy.Members(t, f, w.tn.N.Name)[1]
			f.StopService(t, victim, tenancy.UnitGateway(w.tn.N.Name))
			holdServing(t, w, others(n, victim))
		}},
		kill(pick(2), "rqlite", edge.IndexRQLiteUnit),
		kill(pick(0), "olric", realistic.IndexOlricUnit),
	}
}

func others(all []fleet.Node, not fleet.Node) []fleet.Node {
	var out []fleet.Node
	for _, n := range all {
		if n.Name != not.Name {
			out = append(out, n)
		}
	}
	return out
}

// killAndWait SIGKILLs unit and waits until the product restarted it.
func killAndWait(t *testing.T, f *fleet.Fleet, n fleet.Node, unit string) {
	t.Helper()
	before := tenancy.ActiveSince(t, f, n, unit)
	f.Kill(t, n, unit)
	eventually.Require(t, 2*time.Second, restartBudget, unit+" restarted on "+n.Name, func() (bool, error) {
		return tenancy.ActiveSince(t, f, n, unit) != before && f.Unit(t, n, unit) == "active", nil
	})
}

// holdServing checks, for faultHold, that each of nodes keeps answering
// the namespace's health through its own gateway.
func holdServing(t *testing.T, w *workload, nodes []fleet.Node) {
	t.Helper()
	edge.Hold(t, 10*time.Second, faultHold, "the other nodes serving through the fault", func() (bool, error) {
		for _, n := range nodes {
			r, err := w.tn.C.PinTo(n.PublicIP).Send(t.Context(), gw.Req{Path: "/v1/health"})
			if err != nil || r.Status != http.StatusOK {
				return false, fmt.Errorf("%s: %v %v", n.Name, err, r)
			}
		}
		return true, nil
	})
}

// quiet holds, for d, a converged cluster whose rqlite applied index lag
// stays bounded: between faults nothing may flap.
func quiet(t *testing.T, d time.Duration) {
	t.Helper()
	if d < observeEvery {
		return
	}
	f := harness.Fleet(t)
	cli := harness.CLI(t)
	edge.Hold(t, observeEvery, d, "a converged cluster between faults", func() (bool, error) {
		r, err := monitor.Get(t.Context(), cli, f.State.Env)
		if err != nil {
			return false, err
		}
		if err := r.Converged(len(f.State.Nodes)); err != nil {
			return false, err
		}
		if lag := appliedLag(r); lag > maxAppliedLag {
			return false, fmt.Errorf("rqlite applied index lag %d over %d", lag, maxAppliedLag)
		}
		return true, nil
	})
}

// appliedLag is the spread of rqlite applied indexes across nodes.
func appliedLag(r *monitor.Report) uint64 {
	var lo, hi uint64
	seen := false
	for _, n := range r.Nodes {
		if n.Report == nil || n.Report.RQLite == nil {
			continue
		}
		a := n.Report.RQLite.Applied
		if !seen || a < lo {
			lo = a
		}
		hi, seen = max(hi, a), true
	}
	return hi - lo
}

// fire runs ev and records its window, which ends once the cluster has
// converged again.
func fire(t *testing.T, ws *windows, ev event) {
	t.Helper()
	start := time.Now()
	t.Run(ev.name, ev.run)
	infra.WaitConverged(t, len(harness.Fleet(t).State.Nodes), infra.ConvergeBudget, "the cluster after "+ev.name)
	ws.add(window{Name: ev.name, Start: start, End: time.Now()})
}
