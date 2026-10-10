package setup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/DeBrosOfficial/network/pkg/install"
	"github.com/DeBrosOfficial/network/pkg/statesync"
)

// globalParallelism is how many machines install the global layer at once. The
// installs are independent (each machine's own units and its own state-sync);
// only the restart that follows and the chain transactions are one at a time.
const globalParallelism = 4

// syncStalls is how many polls in a row the chain unit may be down before the
// sync is given up: a unit restarting between two polls is not a failure, one
// that stays down is.
const syncStalls = 3

func (r *runner) fullRuns() []*nodeRun {
	var out []*nodeRun
	for _, n := range r.runs {
		if n.plan.Full() {
			out = append(out, n)
		}
	}
	return out
}

// globalPhase installs the global layer on every full machine that lacks it and
// waits until each chain node has caught up.
func (r *runner) globalPhase(ctx context.Context) error {
	var todo []*nodeRun
	for _, n := range r.fullRuns() {
		if n.facts.GlobalInstalled {
			r.skip(n.plan.IP, StepGlobal, "the chain unit is installed")
			r.skip(n.plan.IP, StepSync, "")
			continue
		}
		todo = append(todo, n)
	}
	if len(todo) == 0 {
		return nil
	}
	trust, err := r.d.Trust.TrustPoint(ctx, r.net)
	if err != nil {
		return fmt.Errorf("find a block to start the chain from: %w", err)
	}
	r.d.Report.Linef("chain starts from block %d (%s), agreed by %d seeds", trust.Height, trust.Hash, len(trust.Servers))
	if err := r.parallel(todo, func(n *nodeRun) error { return r.installGlobal(ctx, n, trust) }); err != nil {
		return err
	}
	return r.parallel(todo, func(n *nodeRun) error { return r.waitSynced(ctx, n) })
}

func (r *runner) parallel(nodes []*nodeRun, fn func(*nodeRun) error) error {
	var g errgroup.Group
	g.SetLimit(globalParallelism)
	for _, n := range nodes {
		g.Go(func() error { return fn(n) })
	}
	return g.Wait()
}

func (r *runner) installGlobal(ctx context.Context, n *nodeRun, trust *statesync.TrustPoint) error {
	ip := n.plan.IP
	r.emit(ip, StepGlobal, StateRunning, strings.Join(n.plan.ServiceNames(), ","))
	in := GlobalInstall{
		Node: n.plan, IP: ip, User: r.opts.User, ChainID: r.net.Manifest.ChainID, Genesis: r.genesis,
		Trust: trust, Contact: r.contact(),
	}
	if n.plan.HasService(install.GlobalServiceRelay) {
		tor, err := os.ReadFile(r.opts.TorNetwork)
		if err != nil {
			return fmt.Errorf("read --tor-network: %w", err)
		}
		in.TorNetwork = tor
	}
	if err := n.m.InstallGlobal(ctx, in); err != nil {
		r.emit(ip, StepGlobal, StateFailed, err.Error())
		return fmt.Errorf("machine %s: install the global layer: %w", ip, err)
	}
	n.globalNew = true
	r.emit(ip, StepGlobal, StateDone, "")
	return nil
}

// contact is where an abuse complaint about a relay goes: the operator's
// --contact, else the operator account.
func (r *runner) contact() string {
	if r.opts.Contact != "" {
		return r.opts.Contact
	}
	return "orama operator " + r.oper
}

// waitSynced polls the chain node until it has restored its snapshot and caught
// up, or the deadline passes. The chain unit being down for several polls in a
// row ends the wait at once, with the end of its log.
func (r *runner) waitSynced(ctx context.Context, n *nodeRun) error {
	ip, down := n.plan.IP, 0
	var lastErr error
	r.emit(ip, StepSync, StateRunning, "restoring a snapshot")
	t := r.d.Timing
	err := pollUntil(ctx, t.SyncPoll, t.SyncDeadline, "the chain on "+ip+" to catch up", func(ctx context.Context) (bool, error) {
		st, err := n.m.ChainState(ctx)
		if err != nil {
			// A poll that cannot reach the node is not an answer; the last one
			// is part of the message if the wait runs out.
			lastErr = err
			return false, nil
		}
		if !st.Running {
			if down++; down >= syncStalls {
				return false, fmt.Errorf("the chain unit on %s is not running (%d polls in a row); the end of its log:\n%s\n"+
					"  if the log asks for a binary of a later upgrade, the network has upgraded since the genesis binary: see the known gaps of the setup docs", ip, down, st.Detail)
			}
			return false, nil
		}
		down = 0
		return st.Height > 0 && !st.CatchingUp, nil
	})
	if err != nil {
		err = errors.Join(err, lastErr)
		r.emit(ip, StepSync, StateFailed, err.Error())
		return fmt.Errorf("machine %s: %w", ip, err)
	}
	r.emit(ip, StepSync, StateDone, "")
	return nil
}

// restartPhase restarts, one machine at a time, the cluster nodes that just got
// the global layer: the cluster gateway reads the chain's listeners when it
// starts. Each restart waits until the node carries its share again before the
// next begins, so two RQLite voters are never down together.
func (r *runner) restartPhase(ctx context.Context) error {
	for _, n := range r.fullRuns() {
		if !n.globalNew {
			r.skip(n.plan.IP, StepRestart, "")
			continue
		}
		r.emit(n.plan.IP, StepRestart, StateRunning, "")
		if err := n.m.RestartNode(ctx, r.d.Timing.RestartBudget, r.clusterTooSmallForQuorum()); err != nil {
			r.emit(n.plan.IP, StepRestart, StateFailed, err.Error())
			return fmt.Errorf("machine %s: restart the cluster node: %w\n  the machines after it were left alone, so the cluster keeps its other voters", n.plan.IP, err)
		}
		r.emit(n.plan.IP, StepRestart, StateDone, "")
	}
	return nil
}

// clusterTooSmallForQuorum says restarting a node cannot keep a quorum: with one
// or two voters, one down is already too many. The node's own check refuses
// then, and --force is the only way a small cluster restarts at all.
func (r *runner) clusterTooSmallForQuorum() bool {
	return r.clusterSize < minQuorumCluster
}

// minQuorumCluster is the smallest cluster that survives one voter restarting.
const minQuorumCluster = 3
