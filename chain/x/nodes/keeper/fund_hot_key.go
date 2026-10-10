package keeper

import (
	"fmt"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/nodes/types"
)

// FundHotKey moves amount from the operator's own earnings account (or, with
// FUND_SOURCE_BANK, from its bank balance) to the fee-only balance of the hot key registered on the operator's own node
// (plans/open-network/track-c-chain.md C2 item 5). The target is always the
// node's registered hot key: the message carries no destination, so there is no
// way to aim earnings at another account, and that key proved it holds itself
// when the node registered it (its "hot-key" binding), so it is not an address
// the operator merely named. A fee-only balance can pay a transaction's base fee
// and nothing else: it is not earnings, so it can never be bonded, shielded,
// deposited or moved on. A retired or tombstoned node no longer has a live hot
// key and is refused.
//
// The bank source is for an operator that holds ORAMA but has earned nothing: a node that runs no
// validator earns nothing until its storage providers have proven, and a provider cannot prove
// without a fee balance for the proof's base fee. Either way the coins end in x/fees's module
// account and the balance is fee-only.
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
		if err := k.fundFeeBalance(ctx, msg.Source, from, to, msg.Amount); err != nil {
			return fmt.Errorf("fund hot key of node %s: %w", node.NodeId, err)
		}
		// The fee balance is for the hot key's own transactions, and an address with no account
		// cannot sign one: the first funding creates it. A funded key keeps its account (and
		// sequence) on every later funding.
		if !k.accountKeeper.HasAccount(ctx, to) {
			k.accountKeeper.SetAccount(ctx, k.accountKeeper.NewAccountWithAddress(ctx, to))
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

// fundFeeBalance credits the hot key's fee-only balance out of the source the message names. The
// source was validated in ValidateBasic; an unknown value is refused again here rather than
// treated as earnings.
func (k Keeper) fundFeeBalance(ctx sdk.Context, source types.FundSource, from, to sdk.AccAddress, amount math.Int) error {
	switch source {
	case types.FundSource_FUND_SOURCE_EARNINGS:
		return k.earningsKeeper.FundFeeBalance(ctx, from, to, amount)
	case types.FundSource_FUND_SOURCE_BANK:
		return k.earningsKeeper.FundFeeBalanceFromBank(ctx, from, to, amount)
	default:
		return fmt.Errorf("unknown funding source %d", int32(source))
	}
}
