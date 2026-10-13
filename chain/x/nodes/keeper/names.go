package keeper

import (
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/nodes/types"
)

// Reasons a name is released, reported on the release event.
const (
	releaseReasonRelease   = "release"
	releaseReasonRetire    = "retire"
	releaseReasonTombstone = "tombstone"
)

// ClaimNodeName claims name for one of the operator's nodes and locks the name deposit from the
// operator's bank balance, topped up from the operator's own earnings if it is short. A node holds
// one name and a name belongs to one node, first come first served. The name is validated again
// here, with the deposit, so a message that skipped ValidateBasic cannot claim a reserved label.
func (k Keeper) ClaimNodeName(ctx sdk.Context, msg *types.MsgClaimNodeName) error {
	if msg == nil {
		return fmt.Errorf("nil MsgClaimNodeName")
	}
	return k.transact(ctx, func(ctx sdk.Context) error {
		if err := msg.ValidateBasic(); err != nil {
			return err
		}
		p, err := k.params(ctx)
		if err != nil {
			return err
		}
		node, operator, err := k.requireNodeOwner(ctx, msg.NodeId, msg.Operator)
		if err != nil {
			return err
		}
		if err := closedNode(node); err != nil {
			return err
		}
		if err := k.requireNameFree(ctx, node.NodeId, msg.Name); err != nil {
			return err
		}
		if err := k.lockNameDeposit(ctx, operator, node.NodeId, p.NameDeposit); err != nil {
			return err
		}
		claim := types.NodeName{Name: msg.Name, NodeId: node.NodeId, Operator: operator, Deposit: p.NameDeposit}
		if err := k.indexName(ctx, claim); err != nil {
			return err
		}
		ctx.EventManager().EmitEvent(sdk.NewEvent(types.EventTypeClaimNodeName,
			sdk.NewAttribute(types.AttributeOperator, operator),
			sdk.NewAttribute(types.AttributeNodeID, node.NodeId),
			sdk.NewAttribute(types.AttributeName, claim.Name),
			sdk.NewAttribute(types.AttributeDeposit, claim.Deposit.String()),
		))
		return nil
	})
}

// ReleaseNodeName gives the node's name up and returns its deposit to the operator.
func (k Keeper) ReleaseNodeName(ctx sdk.Context, msg *types.MsgReleaseNodeName) error {
	if msg == nil {
		return fmt.Errorf("nil MsgReleaseNodeName")
	}
	return k.transact(ctx, func(ctx sdk.Context) error {
		if err := msg.ValidateBasic(); err != nil {
			return err
		}
		node, _, err := k.requireNodeOwner(ctx, msg.NodeId, msg.Operator)
		if err != nil {
			return err
		}
		released, err := k.releaseNodeName(ctx, node.NodeId, releaseReasonRelease)
		if err != nil {
			return err
		}
		if !released {
			return fmt.Errorf("node %s: %w", node.NodeId, types.ErrNoName)
		}
		return nil
	})
}

// requireNameFree refuses a claim when the node already holds a name or the name is taken.
func (k Keeper) requireNameFree(ctx sdk.Context, nodeID, name string) error {
	held, err := k.NodeNames.Get(ctx, nodeID)
	if err == nil {
		return fmt.Errorf("node %s already holds %q, release it first: %w", nodeID, held, types.ErrNodeHasName)
	}
	if !errors.Is(err, collections.ErrNotFound) {
		return fmt.Errorf("load the name of node %s: %w", nodeID, err)
	}
	owner, err := k.Names.Get(ctx, name)
	if err == nil {
		return fmt.Errorf("%q belongs to node %s: %w", name, owner.NodeId, types.ErrNameTaken)
	}
	if !errors.Is(err, collections.ErrNotFound) {
		return fmt.Errorf("load node name %q: %w", name, err)
	}
	return nil
}

// lockNameDeposit moves the deposit from the operator into the nodes module account. Funding it
// from the operator's own earnings happens here, in the message's branch, like a role bond.
func (k Keeper) lockNameDeposit(ctx sdk.Context, operator, nodeID string, deposit math.Int) error {
	owner, err := sdk.AccAddressFromBech32(operator)
	if err != nil {
		return fmt.Errorf("operator %s: %w", operator, err)
	}
	coins, err := norama(deposit)
	if err != nil {
		return err
	}
	if err := k.earningsKeeper.FundSpendFromEarnings(ctx, owner, params.BaseDenom, deposit); err != nil {
		return fmt.Errorf("name deposit for node %s: %w", nodeID, err)
	}
	if err := k.bankKeeper.SendCoinsFromAccountToModule(ctx, owner, types.ModuleName, coins); err != nil {
		return fmt.Errorf("name deposit for node %s: %w", nodeID, err)
	}
	return nil
}

// indexName records a claim under its name and its node.
func (k Keeper) indexName(ctx sdk.Context, claim types.NodeName) error {
	if err := k.Names.Set(ctx, claim.Name, claim); err != nil {
		return fmt.Errorf("index node name %q: %w", claim.Name, err)
	}
	if err := k.NodeNames.Set(ctx, claim.NodeId, claim.Name); err != nil {
		return fmt.Errorf("index the name of node %s: %w", claim.NodeId, err)
	}
	return nil
}

// releaseNodeName removes the node's name, if it holds one, and returns the deposit to the
// operator who locked it. It reports whether there was a name to release.
func (k Keeper) releaseNodeName(ctx sdk.Context, nodeID, reason string) (bool, error) {
	name, err := k.NodeNames.Get(ctx, nodeID)
	if errors.Is(err, collections.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("load the name of node %s: %w", nodeID, err)
	}
	claim, err := k.Names.Get(ctx, name)
	if err != nil {
		return false, fmt.Errorf("load node name %q: %w", name, err)
	}
	owner, err := sdk.AccAddressFromBech32(claim.Operator)
	if err != nil {
		return false, fmt.Errorf("operator %s: %w", claim.Operator, err)
	}
	coins, err := norama(claim.Deposit)
	if err != nil {
		return false, err
	}
	if err := k.bankKeeper.SendCoinsFromModuleToAccount(ctx, types.ModuleName, owner, coins); err != nil {
		return false, fmt.Errorf("refund the deposit of name %q to %s: %w", name, claim.Operator, err)
	}
	if err := k.Names.Remove(ctx, name); err != nil {
		return false, fmt.Errorf("remove node name %q: %w", name, err)
	}
	if err := k.NodeNames.Remove(ctx, nodeID); err != nil {
		return false, fmt.Errorf("remove the name of node %s: %w", nodeID, err)
	}
	ctx.EventManager().EmitEvent(sdk.NewEvent(types.EventTypeReleaseNodeName,
		sdk.NewAttribute(types.AttributeOperator, claim.Operator),
		sdk.NewAttribute(types.AttributeNodeID, nodeID),
		sdk.NewAttribute(types.AttributeName, name),
		sdk.NewAttribute(types.AttributeDeposit, claim.Deposit.String()),
		sdk.NewAttribute(types.AttributeReason, reason),
	))
	return true, nil
}

// sumNameDeposits adds up the deposits of every claimed name: the part of the nodes module account
// that is neither a role bond nor unbonding escrow.
func (k Keeper) sumNameDeposits(ctx sdk.Context) (math.Int, error) {
	sum := math.ZeroInt()
	err := k.Names.Walk(ctx, nil, func(_ string, claim types.NodeName) (bool, error) {
		if claim.Deposit.IsNil() || !claim.Deposit.IsPositive() {
			return true, fmt.Errorf("deposit of name %q is invalid", claim.Name)
		}
		sum = sum.Add(claim.Deposit)
		return false, nil
	})
	if err != nil {
		return math.Int{}, fmt.Errorf("sum name deposits: %w", err)
	}
	return sum, nil
}
