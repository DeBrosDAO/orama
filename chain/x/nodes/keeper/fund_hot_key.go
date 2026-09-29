package keeper

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/nodes/types"
)

// FundHotKey moves amount from the operator's own earnings account to the
// fee-only balance of the hot key registered on the operator's own node
// (plans/open-network/track-c-chain.md C2 item 5). The target is always the
// node's registered hot key: the message carries no destination, so there is no
// way to aim earnings at another account, and that key proved it holds itself
// when the node registered it (its "hot-key" binding), so it is not an address
// the operator merely named. A fee-only balance can pay a transaction's base fee
// and nothing else: it is not earnings, so it can never be bonded, shielded,
// deposited or moved on. A retired or tombstoned node no longer has a live hot
// key and is refused.
func (k Keeper) FundHotKey(ctx sdk.Context, msg *types.MsgFundHotKey) error {
	if msg == nil {
		return fmt.Errorf("nil MsgFundHotKey")
	}
	return k.transact(ctx, func(ctx sdk.Context) error {
		if err := msg.ValidateBasic(); err != nil {
			return err
		}
		node, operator, err := k.requireNodeOwner(ctx, msg.NodeId, msg.Operator)
		if err != nil {
			return err
		}
		if err := closedNode(node); err != nil {
			return err
		}
		from, err := sdk.AccAddressFromBech32(operator)
		if err != nil {
			return fmt.Errorf("operator %s: %w", operator, err)
		}
		to, err := sdk.AccAddressFromBech32(node.HotKey)
		if err != nil {
			return fmt.Errorf("hot key of node %s: %w", node.NodeId, err)
		}
		if err := k.earningsKeeper.FundFeeBalance(ctx, from, to, msg.Amount); err != nil {
			return fmt.Errorf("fund hot key of node %s: %w", node.NodeId, err)
		}
		ctx.EventManager().EmitEvent(sdk.NewEvent(types.EventTypeFundHotKey,
			sdk.NewAttribute(types.AttributeOperator, operator),
			sdk.NewAttribute(types.AttributeNodeID, node.NodeId),
			sdk.NewAttribute(types.AttributeHotKey, node.HotKey),
			sdk.NewAttribute(types.AttributeAmount, msg.Amount.String()),
		))
		return nil
	})
}
