package keeper

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/storage/types"
)

// BeginBlock reconciles the tracked node set with x/nodes, assigns due deals, opens this epoch's challenges, and closes the
// previous epoch when x/emission's epoch counter moves.
func (k Keeper) BeginBlock(ctx sdk.Context) error {
	if err := k.syncNodes(ctx); err != nil {
		return err
	}
	epoch, err := k.currentEpoch(ctx)
	if err != nil {
		return err
	}
	last, err := k.LastEpoch.Get(ctx)
	if err != nil {
		return fmt.Errorf("failed to load last storage epoch: %w", err)
	}
	if epoch > last {
		if err := k.closeEpoch(ctx, last); err != nil {
			return err
		}
		if err := k.LastEpoch.Set(ctx, epoch); err != nil {
			return err
		}
		if err := k.maybeProtocolDeals(ctx, epoch); err != nil {
			return err
		}
	}
	if err := k.assignDue(ctx); err != nil {
		return err
	}
	if err := k.expireAcceptWindows(ctx); err != nil {
		return err
	}
	if err := k.openChallenges(ctx, epoch); err != nil {
		return err
	}
	if err := k.expireDeals(ctx, epoch); err != nil {
		return err
	}
	return k.expireProbation(ctx, epoch)
}

// EndBlock drains the settlement queue up to its per-block limit, then expires
// deals whose payments have finished landing.
func (k Keeper) EndBlock(ctx sdk.Context) error {
	if err := k.SettleQueue(ctx); err != nil {
		return err
	}
	epoch, err := k.currentEpoch(ctx)
	if err != nil {
		return err
	}
	return k.expireDeals(ctx, epoch)
}

// TrackNode records a node id so the assignment index can see it. syncNodes calls
// it, from BeginBlock, for a node x/nodes has queued and that has become eligible.
// probation marks a fee-free registration.
func (k Keeper) TrackNode(ctx sdk.Context, nodeID string, probation bool) error {
	if nodeID == "" || len(nodeID) > 128 {
		return fmt.Errorf("invalid node id %q", nodeID)
	}
	epoch, err := k.currentEpoch(ctx)
	if err != nil {
		return err
	}
	state := types.NodeState{NodeId: nodeID, Probation: probation, RegisteredEpoch: epoch}
	if err := k.Nodes.Set(ctx, nodeID, state); err != nil {
		return fmt.Errorf("failed to track node %s: %w", nodeID, err)
	}
	return nil
}
