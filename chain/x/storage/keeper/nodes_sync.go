package keeper

import (
	"errors"
	"fmt"

	"cosmossdk.io/collections"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/storage/types"
)

// syncNodes reconciles x/storage's tracked node set with x/nodes. x/nodes queues every
// STORAGE-role node it writes (bond, unbond, jail, retire, slash, capacity, endpoints); this drains
// that queue, so the work is proportional to what changed, never to the node count.
//
// A node is tracked while NodeView.IsActive holds: its STORAGE role is bonded at min_bond and it is
// not jailed, retired or tombstoned. A fee-free probation registration (NodeView.IsProbation: the
// role, no bond, not jailed) is tracked as a probation node instead, so it can take the few
// protocol-deal slots probation allows (C7). A probation node that bonds graduates on the spot: it
// is an ordinary provider from then on. A node that stops qualifying is untracked only once it holds
// no replicas: challenge sampling and settlement still read the state of a node that holds slots,
// so such a node stays tracked (assignment already skips it, because it is not active) and is
// re-queued until its last replica is released, evicted or expired. A probation node that
// graduated without a bond stays tracked, without slots, until it bonds; it does not start a
// second probation.
//
// One node's record can be unreadable or inconsistent (a node that x/nodes cannot resolve, a
// deposit that will not release). That is data about one node, not a reason to stop the chain: the
// node's work is rolled back, the node stays queued so the next block retries it, and a
// storage_node_sync_failed event carries the reason. Only the queue itself failing to be read or
// re-written is returned, since then the tracked set can no longer be trusted.
func (k Keeper) syncNodes(ctx sdk.Context) error {
	ids, err := k.nodes.TakeStorageChanges(ctx)
	if err != nil {
		return fmt.Errorf("failed to read x/nodes storage changes: %w", err)
	}
	for _, id := range ids {
		nodeCtx, write := ctx.CacheContext()
		if err := k.syncNode(nodeCtx, id); err != nil {
			if qerr := k.nodes.MarkStorageChanged(ctx, id); qerr != nil {
				return fmt.Errorf("failed to re-queue %s after a failed reconciliation (%v): %w", id, err, qerr)
			}
			k.Logger(ctx).Error("storage node reconciliation failed; retrying next block", "node", id, "err", err)
			ctx.EventManager().EmitEvent(sdk.NewEvent("storage_node_sync_failed",
				sdk.NewAttribute("node_id", id),
				sdk.NewAttribute("error", err.Error()),
			))
			continue
		}
		write()
	}
	return nil
}

// syncNode reconciles one node id with x/nodes.
func (k Keeper) syncNode(ctx sdk.Context, id string) error {
	eligible, err := k.nodes.IsActive(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to read storage eligibility of %s: %w", id, err)
	}
	state, err := k.Nodes.Get(ctx, id)
	if err != nil && !errors.Is(err, collections.ErrNotFound) {
		return fmt.Errorf("failed to read tracked state of %s: %w", id, err)
	}
	tracked := err == nil
	if eligible {
		switch {
		case !tracked:
			return k.TrackNode(ctx, id, false)
		case state.Probation:
			return k.endProbation(ctx, state, true)
		}
		return nil
	}
	probation := false
	if !tracked || (state.Probation && !state.Graduated) {
		if probation, err = k.nodes.IsProbation(ctx, id); err != nil {
			return fmt.Errorf("failed to read probation status of %s: %w", id, err)
		}
	}
	switch {
	case !tracked && probation:
		return k.TrackNode(ctx, id, true)
	case tracked && state.Probation && probation, tracked && state.Graduated:
		return nil
	case tracked:
		return k.untrackIdleNode(ctx, id)
	}
	return nil
}

// untrackIdleNode drops a node's tracked state when it holds no replicas, recovering its probation
// record deposit if one is locked. A node that still holds replicas is queued again.
func (k Keeper) untrackIdleNode(ctx sdk.Context, nodeID string) error {
	held, err := k.replicasHeld(ctx, nodeID)
	if err != nil {
		return err
	}
	if held > 0 {
		return k.nodes.MarkStorageChanged(ctx, nodeID)
	}
	state, err := k.Nodes.Get(ctx, nodeID)
	if err != nil {
		return fmt.Errorf("failed to load tracked state of %s: %w", nodeID, err)
	}
	if state.DepositLocked {
		if _, _, err := k.deposits.ReleaseDeposit(ctx, probationDepositID(nodeID)); err != nil {
			return fmt.Errorf("failed to recover probation deposit of %s: %w", nodeID, err)
		}
	}
	if err := k.Nodes.Remove(ctx, nodeID); err != nil {
		return fmt.Errorf("failed to untrack %s: %w", nodeID, err)
	}
	return nil
}

// endProbation takes a node out of probation. A slot it holds no longer counts against the probation
// caps, and a locked record deposit is recovered. graduated marks it as a proven provider (it bonded,
// or it proved storage and outlived probation); otherwise it stays a plain tracked node that will
// be jailed or untracked by the caller.
func (k Keeper) endProbation(ctx sdk.Context, state types.NodeState, graduated bool) error {
	if err := k.releaseProbationCounters(ctx, state); err != nil {
		return err
	}
	if state.DepositLocked {
		if _, _, err := k.deposits.ReleaseDeposit(ctx, probationDepositID(state.NodeId)); err != nil {
			return fmt.Errorf("failed to recover probation deposit of %s: %w", state.NodeId, err)
		}
		state.DepositLocked = false
	}
	state.Probation = false
	state.Graduated = graduated
	if err := k.Nodes.Set(ctx, state.NodeId, state); err != nil {
		return fmt.Errorf("failed to end probation of %s: %w", state.NodeId, err)
	}
	return nil
}

// releaseProbationCounters removes the probation-cap counts of every protocol-deal slot the node
// holds. The counts are taken at assignment and released at detach, and detach releases only for a
// node still on probation, so a node that leaves probation while it holds slots must release them
// here or its operator, /16 and ASN stay charged for slots it no longer counts.
func (k Keeper) releaseProbationCounters(ctx sdk.Context, state types.NodeState) error {
	var refs []types.SlotRef
	rng := collections.NewPrefixedPairRange[string, uint64](state.NodeId)
	if err := k.ReplicaAt.Walk(ctx, rng, func(_ collections.Pair[string, uint64], ref types.SlotRef) (bool, error) {
		refs = append(refs, ref)
		return false, nil
	}); err != nil {
		return fmt.Errorf("failed to list replicas of %s: %w", state.NodeId, err)
	}
	for _, ref := range refs {
		deal, err := k.loadDeal(ctx, ref.DealId)
		if err != nil {
			return err
		}
		if !deal.Protocol {
			continue
		}
		slot, err := k.loadSlot(ctx, ref.DealId, ref.Slot)
		if err != nil {
			return err
		}
		if err := k.noteProbationAssign(ctx, state, slot.Operator, slot.Network16, slot.Asn, -1); err != nil {
			return err
		}
	}
	return nil
}
