package node

import (
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/node/boot"
	"github.com/DeBrosOfficial/network/pkg/node/lifecycle"
	"go.uber.org/zap"
)

// servingCore names the components a node must have converged before it can
// claim to be serving anything at all: its local database and the gateway
// everything else proxies to.
//
// It is deliberately narrower than "the whole local tier". Pinning `joining` on
// every local component would let one component that can never converge — a
// broken ntfy inside `edge`, say — hold the node out of `IsAvailable` forever,
// so it would also never announce maintenance on shutdown. A node whose
// database and gateway are up is serving; anything still missing after that is
// what `degraded` is for.
var servingCore = []string{compRQLiteLocal, compGateway}

// nextLifecycleState maps a boot snapshot onto the lifecycle state the node
// should announce.
//
// The rules, in order:
//   - draining and maintenance are operator-driven; the supervisor never
//     overrides them.
//   - everything converged means active.
//   - a node that has never served and has not yet brought up its serving core
//     is still joining — degraded is a claim about a node that is answering.
//   - anything else is degraded: serving, not fully converged.
func nextLifecycleState(current lifecycle.State, snap boot.Snapshot) (lifecycle.State, bool) {
	switch current {
	case lifecycle.StateDraining, lifecycle.StateMaintenance:
		return current, false
	}

	want := lifecycle.StateDegraded
	switch {
	case snap.AllReady():
		want = lifecycle.StateActive
	case current == lifecycle.StateJoining && !servingCoreReady(snap):
		want = lifecycle.StateJoining
	}

	return want, want != current
}

// servingCoreReady reports whether every component in servingCore has converged.
func servingCoreReady(snap boot.Snapshot) bool {
	ready := 0
	for _, c := range snap.Components {
		if c.Status != boot.StatusReady {
			continue
		}
		for _, name := range servingCore {
			if c.Name == name {
				ready++
			}
		}
	}
	return ready == len(servingCore)
}

// applyBootState moves the lifecycle state to match the supervisor's view and
// republishes the node's discovery metadata when it changes.
func (n *Node) applyBootState(snap boot.Snapshot) {
	// Once Stop has begun, the lifecycle belongs to the shutdown path. Without
	// this the supervisor could read "degraded", have Stop announce maintenance
	// underneath it, and then transition the dying node back to active and
	// re-publish it to peers as available.
	if n.stopping.Load() {
		return
	}

	current := n.lifecycle.State()
	want, change := nextLifecycleState(current, snap)
	if !change {
		return
	}

	// Conditional on `current`: Stop can announce maintenance between the read
	// above and the write below, and an unconditional transition would undo it
	// and re-publish a dying node to its peers as available.
	if err := n.lifecycle.TransitionToFrom(current, want); err != nil {
		n.logger.ComponentWarn(logging.ComponentNode, "Failed to apply boot lifecycle state",
			zap.String("target", string(want)), zap.Error(err))
		return
	}

	n.logger.ComponentInfo(logging.ComponentNode, "Node lifecycle state changed",
		zap.String("state", string(want)),
		zap.Strings("not_converged", snap.NotReady()))

	if cd := n.getClusterDiscovery(); cd != nil {
		cd.UpdateOwnMetadata()
	}
}
