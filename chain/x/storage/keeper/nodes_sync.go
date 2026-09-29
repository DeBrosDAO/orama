package keeper

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// syncNodes reconciles x/storage's tracked node set with x/nodes. x/nodes queues every
// STORAGE-role node it writes (bond, unbond, jail, retire, slash, capacity, endpoints); this drains
// that queue, so the work is proportional to what changed, never to the node count.
//
// A node is tracked while NodeView.IsActive holds: its STORAGE role is bonded at min_bond and it is
// not jailed, retired or tombstoned. A node that stops qualifying is untracked only once it holds
// no replicas: challenge sampling and settlement still read the state of a node that holds slots,
// so such a node stays tracked (assignment already skips it, because it is not active) and is
// re-queued until its last replica is released, evicted or expired.
func (k Keeper) syncNodes(ctx sdk.Context) error {
	ids, err := k.nodes.TakeStorageChanges(ctx)
	if err != nil {
		return fmt.Errorf("failed to read x/nodes storage changes: %w", err)
	}
	for _, id := range ids {
		eligible, err := k.nodes.IsActive(ctx, id)
		if err != nil {
			return fmt.Errorf("failed to read storage eligibility of %s: %w", id, err)
		}
		tracked, err := k.Nodes.Has(ctx, id)
		if err != nil {
			return fmt.Errorf("failed to read tracked state of %s: %w", id, err)
		}
		switch {
		case eligible && !tracked:
			if err := k.TrackNode(ctx, id, false); err != nil {
				return err
			}
		case !eligible && tracked:
			if err := k.untrackIdleNode(ctx, id); err != nil {
				return err
			}
		}
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
