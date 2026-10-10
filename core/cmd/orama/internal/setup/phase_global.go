package setup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/DeBrosOfficial/network/pkg/install"
	"github.com/DeBrosOfficial/network/pkg/statesync"
	"github.com/DeBrosOfficial/network/pkg/tornet"
)

const (
	// maxGlobalParallelism is the most machines that install the global layer at
	// once. The installs are independent (each machine's own units and its own
	// state-sync); only the restart that follows and the chain transactions are one
	// at a time.
	maxGlobalParallelism = 4
	// syncStalls is how many polls in a row the chain unit may be down before the
	// sync is given up: a unit restarting between two polls is not a failure, one
	// that stays down is.
	syncStalls = 3
	// syncPollErrors is how many polls in a row may fail to reach the machine or
	// to read its answer before the wait is given up.
	syncPollErrors = 5
	// minQuorumCluster is the smallest cluster that survives one voter restarting.
	minQuorumCluster = 3
)

// globalParallelism is how many machines install at once on a cluster of size
// nodes: at most as many as the cluster can lose and still hold its quorum, so
// that heavy installs (a package install, units, firewall rules) never run on
// every voter together, and never more than maxGlobalParallelism.
func globalParallelism(size int) int {
	spare := size - (size/2 + 1)
	switch {
	case spare < 1:
		return 1
	case spare > maxGlobalParallelism:
		return maxGlobalParallelism
	}
	return spare
}

func (r *runner) fullRuns() []*nodeRun {
	var out []*nodeRun
	for _, n := range r.runs {
		if n.plan.Full() {
			out = append(out, n)
		}
	}
	return out
}

// globalPhase installs the global layer on every full machine that lacks it, and
// on every one waits until its chain has caught up. A machine that has the layer
// but whose chain is not running (a run stopped between the install and the start)
// is started; one that is running goes straight to the wait, which returns at its
// first poll when the chain is synced.
func (r *runner) globalPhase(ctx context.Context) error {
	var fresh []*nodeRun
	for _, n := range r.fullRuns() {
		if !n.facts.GlobalInstalled {
			fresh = append(fresh, n)
			continue
		}
		if err := r.resumeGlobal(ctx, n); err != nil {
			return err
		}
	}
	if len(fresh) > 0 {
		trust, err := r.d.Trust.TrustPoint(ctx, r.net)
		if err != nil {
			return fmt.Errorf("find a block to start the chain from: %w", err)
		}
		r.d.Report.Linef("chain starts from block %d (%s), agreed by %d seeds", trust.Height, trust.Hash, len(trust.Servers))
		if err := r.parallel(fresh, func(n *nodeRun) error { return r.installGlobal(ctx, n, trust) }); err != nil {
			return err
		}
	}
	return r.parallel(r.fullRuns(), func(n *nodeRun) error { return r.waitSynced(ctx, n) })
}

// resumeGlobal makes sure the services of a machine that has the global layer are
// running.
func (r *runner) resumeGlobal(ctx context.Context, n *nodeRun) error {
	if n.facts.ChainActive {
		r.skip(n.plan.IP, StepGlobal, "installed and the chain is running")
		return nil
	}
	r.emit(n.plan.IP, StepGlobal, StateRunning, "installed, starting the services")
	if err := n.m.StartGlobal(ctx, n.plan); err != nil {
		r.emit(n.plan.IP, StepGlobal, StateFailed, err.Error())
		return fmt.Errorf("machine %s: %w", n.plan.IP, err)
	}
	r.emit(n.plan.IP, StepGlobal, StateDone, "started")
	return nil
}

func (r *runner) parallel(nodes []*nodeRun, fn func(*nodeRun) error) error {
	var g errgroup.Group
	g.SetLimit(globalParallelism(r.clusterSize))
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
		tor, err := readTorNetwork(r.opts.TorNetwork)
		if err != nil {
			return err
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

// readTorNetwork reads the Tor network file, at most tornet.NetworkFileLimit bytes.
func readTorNetwork(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read --tor-network: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, tornet.NetworkFileLimit+1))
	if err != nil {
		return nil, fmt.Errorf("read --tor-network %s: %w", path, err)
	}
	if len(data) > tornet.NetworkFileLimit {
		return nil, fmt.Errorf("--tor-network %s is larger than the %d bytes a Tor network file can be", path, tornet.NetworkFileLimit)
	}
	return data, nil
}

// contact is where an abuse complaint about a relay goes: the operator's
// --contact, else the operator account.
func (r *runner) contact() string {
	if r.opts.Contact != "" {
		return r.opts.Contact
	}
	return "operator " + r.oper
}

// waitSynced polls the chain node until it has restored its snapshot and caught
// up, or the deadline passes. The chain unit being down for several polls in a
// row ends the wait at once, with the end of its log.
func (r *runner) waitSynced(ctx context.Context, n *nodeRun) error {
	ip, down, failed := n.plan.IP, 0, 0
	var lastErr error
	r.emit(ip, StepSync, StateRunning, "restoring a snapshot")
	t := r.d.Timing
	err := pollUntil(ctx, t.SyncPoll, t.SyncDeadline, "the chain on "+ip+" to catch up", func(ctx context.Context) (bool, error) {
		st, err := n.m.ChainState(ctx)
		if err != nil {
			// A poll that cannot reach the node is not an answer; the last one
			// is part of the message if the wait ends, and several in a row end it.
			lastErr = err
			if failed++; failed >= syncPollErrors {
				return false, fmt.Errorf("the chain on %s could not be polled %d times in a row: %w", ip, failed, err)
			}
			return false, nil
		}
		failed = 0
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
		if !n.globalNew && !n.facts.RestartPending {
			r.skip(n.plan.IP, StepRestart, "the cluster node started after the chain was installed")
			continue
		}
		r.emit(n.plan.IP, StepRestart, StateRunning, "")
		if err := r.restartNode(ctx, n); err != nil {
			r.emit(n.plan.IP, StepRestart, StateFailed, err.Error())
			return fmt.Errorf("machine %s: restart the cluster node: %w\n  the machines after it were left alone, so the cluster keeps its other voters", n.plan.IP, err)
		}
		r.emit(n.plan.IP, StepRestart, StateDone, "")
	}
	return nil
}

// restartNode restarts the cluster node under the node's own quorum check. Only a
// node that refuses, on a cluster the CLI records as under three nodes (which
// cannot keep a quorum through any restart), is restarted again with --force: the
// check is the node's own reading of its voters, and the record is only a count
// of the nodes this CLI installed.
func (r *runner) restartNode(ctx context.Context, n *nodeRun) error {
	budget := r.d.Timing.RestartBudget
	err := n.m.RestartNode(ctx, budget, false)
	if err == nil || !r.clusterTooSmallForQuorum() || !strings.Contains(err.Error(), quorumRefusalHint) {
		return err
	}
	r.d.Report.Linef("the cluster has %d recorded node(s), too few to keep a quorum through a restart: restarting %s with --force", r.clusterSize, n.plan.IP)
	return n.m.RestartNode(ctx, budget, true)
}

// quorumRefusalHint is what `orama node restart` says when its quorum check
// refuses: "Use 'orama node restart --force' to proceed anyway".
const quorumRefusalHint = "node restart --force"

// clusterTooSmallForQuorum says restarting a node cannot keep a quorum: with one
// or two voters, one down is already too many. The node's own check refuses
// then, and --force is the only way a small cluster restarts at all.
func (r *runner) clusterTooSmallForQuorum() bool {
	return r.clusterSize < minQuorumCluster
}
