package setup

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/install"
)

const (
	// createFirstBlocks is the height every chain must reach before the creation
	// goes on: the genesis block, the block vote extensions turn on at
	// (voteExtensionsEnableHeight) and one after it.
	createFirstBlocks = 3
	// createMinEpoch is the epoch the chain must be in before the operator is
	// registered: the seats earn from the first epoch that closes, and the faucet
	// is signed with a seat's earnings.
	createMinEpoch = 2
)

// chainStartPhase wires every machine to the other seats, starts the chains one
// at a time, each after the previous answers, and waits until they all produce
// blocks. A chain of n seats needs more than two thirds of them before the first
// block, so the later starts are what make the earlier ones advance; the gate
// between them only makes sure each unit is up and answering. A machine whose
// chain already runs is left alone.
func (r *runner) chainStartPhase(ctx context.Context) error {
	nodes := r.fullRuns()
	var todo []*nodeRun
	for _, n := range nodes {
		if n.facts.ChainActive {
			r.skip(n.plan.IP, StepSync, "the chain is running")
			continue
		}
		todo = append(todo, n)
	}
	if err := r.parallel(todo, func(n *nodeRun) error { return r.wireChain(ctx, n) }); err != nil {
		return err
	}
	for _, n := range todo {
		if err := r.startChain(ctx, n); err != nil {
			return err
		}
	}
	return r.parallel(nodes, func(n *nodeRun) error { return r.waitBlocks(ctx, n) })
}

// peersOf are the persistent peers of n: every other seat, by node id and public
// address.
func (r *runner) peersOf(n *nodeRun) (string, error) {
	var peers []string
	for _, o := range r.fullRuns() {
		if o != n {
			peers = append(peers, fmt.Sprintf("%s@%s:%d", r.create.seats[o].NodeID, o.plan.IP, constants.ChainP2PPort))
		}
	}
	list := strings.Join(peers, ",")
	if err := install.ValidatePersistentPeers(list); err != nil {
		return "", fmt.Errorf("the persistent peers of %s: %w", n.plan.IP, err)
	}
	return list, nil
}

func (r *runner) wireChain(ctx context.Context, n *nodeRun) error {
	ip := n.plan.IP
	peers, err := r.peersOf(n)
	if err != nil {
		return err
	}
	r.emit(ip, StepSync, StateRunning, "wiring the chain to the other seats")
	if err := r.create.boots[n].WireChain(ctx, WireInput{Node: n.plan, IP: ip, User: r.opts.User, Peers: peers, Contact: r.contact()}); err != nil {
		r.emit(ip, StepSync, StateFailed, err.Error())
		return fmt.Errorf("machine %s: %w", ip, err)
	}
	return nil
}

// startChain starts the machine's global services and waits until the chain's
// unit is active and its RPC answers.
func (r *runner) startChain(ctx context.Context, n *nodeRun) error {
	ip := n.plan.IP
	r.emit(ip, StepSync, StateRunning, "starting the chain")
	if err := n.m.StartGlobal(ctx, n.plan); err != nil {
		r.emit(ip, StepSync, StateFailed, err.Error())
		return fmt.Errorf("machine %s: %w", ip, err)
	}
	err := r.pollChain(ctx, n, r.d.Timing.ReadyBudget, "the chain's RPC", func(h ChainHealth) bool { return h.RPCUp })
	if err != nil {
		r.emit(ip, StepSync, StateFailed, err.Error())
	}
	return err
}

// waitBlocks waits until the chain produces blocks and is not catching up.
func (r *runner) waitBlocks(ctx context.Context, n *nodeRun) error {
	ip := n.plan.IP
	r.emit(ip, StepSync, StateRunning, fmt.Sprintf("waiting for block %d", createFirstBlocks))
	err := r.pollChain(ctx, n, r.d.Timing.SyncDeadline, fmt.Sprintf("block %d", createFirstBlocks), func(h ChainHealth) bool {
		return h.RPCUp && h.Height >= createFirstBlocks && !h.CatchingUp
	})
	if err != nil {
		r.emit(ip, StepSync, StateFailed, err.Error())
		return err
	}
	r.emit(ip, StepSync, StateDone, "")
	return nil
}

// pollChain polls a machine's chain until ready says so. The chain unit down for
// several polls in a row, or a machine that cannot be polled several times in a
// row, ends the wait at once, with the end of the chain's log.
func (r *runner) pollChain(ctx context.Context, n *nodeRun, deadline time.Duration, what string, ready func(ChainHealth) bool) error {
	ip, down, failed := n.plan.IP, 0, 0
	var lastErr error
	err := pollUntil(ctx, r.d.Timing.SyncPoll, deadline, what+" on "+ip, func(ctx context.Context) (bool, error) {
		h, err := r.create.boots[n].ChainHealth(ctx)
		if err != nil {
			lastErr = err
			if failed++; failed >= syncPollErrors {
				return false, fmt.Errorf("the chain on %s could not be polled %d times in a row: %w", ip, failed, err)
			}
			return false, nil
		}
		failed = 0
		if !h.Running {
			if down++; down >= syncStalls {
				return false, fmt.Errorf("the chain unit on %s is not running (%d polls in a row); the end of its log, quoted:\n%s", ip, down, quoteLog(h.Detail))
			}
			return false, nil
		}
		down = 0
		return ready(h), nil
	})
	if err != nil {
		return fmt.Errorf("machine %s: %w", ip, errors.Join(err, lastErr))
	}
	return nil
}

// epochPhase waits until the chain is in the epoch the faucet can be paid from.
// A network without a faucet needs no wait: the operator account is funded by
// hand, and the registration says how much.
func (r *runner) epochPhase(ctx context.Context) error {
	if !r.net.Manifest.Faucet {
		return nil
	}
	first := r.fullRuns()[0]
	r.d.Report.Linef("waiting for epoch %d: the seats earn from the first epoch that closes, and the faucet is paid from their earnings", createMinEpoch)
	var lastErr error
	err := pollUntil(ctx, r.d.Timing.SyncPoll, r.d.Timing.SyncDeadline, fmt.Sprintf("the chain to reach epoch %d", createMinEpoch), func(ctx context.Context) (bool, error) {
		epoch, err := r.create.boots[first].Epoch(ctx)
		if err != nil {
			// A poll that cannot be answered is not an answer; the last one is part
			// of the message if the wait ends.
			lastErr = err
			return false, nil
		}
		return epoch >= createMinEpoch, nil
	})
	if err != nil {
		return fmt.Errorf("machine %s: %w", first.plan.IP, errors.Join(err, lastErr))
	}
	return nil
}
