package keeper

import (
	"fmt"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	nodestypes "github.com/DeBrosOfficial/network/chain/x/nodes/types"
	"github.com/DeBrosOfficial/network/chain/x/shielded/pool"
	"github.com/DeBrosOfficial/network/chain/x/shielded/types"
)

// ExecuteUnshield applies an admitted MsgUnshield. The bundle's notes are spent and its amount
// leaves the pool at once (the turnstile); what happens to the coins depends on the target and on
// the 24h cap. It reports whether the unshield joined the queue.
//
// A fee top-up over the cap fails the whole tx: it is not queued. A bond over the cap, or behind a
// non-empty queue, queues: its notes are spent and its coins stay in the module account until a
// window pays them.
func (k Keeper) ExecuteUnshield(ctx sdk.Context, msg *types.MsgUnshield, signer sdk.AccAddress, adm *Admitted) (bool, error) {
	if msg.Target == types.UnshieldTargetDeposit || msg.Target == types.UnshieldTargetContract {
		return false, fmt.Errorf("%w: %s", types.ErrTargetNotLinked, msg.Target)
	}
	p, err := k.params(ctx)
	if err != nil {
		return false, err
	}
	net := adm.Amount.Sub(adm.NullifierFees)
	if msg.Target == types.UnshieldTargetFeeTopup && net.GT(p.MaxFeeTopup) {
		return false, fmt.Errorf("%w: %s%s over %s%s", types.ErrFeeTopupCap, net, params.BaseDenom, p.MaxFeeTopup, params.BaseDenom)
	}
	if msg.Target == types.UnshieldTargetBond {
		if err := k.deps.Bonder.CheckMinimum(ctx, signer, msg.Validator, net); err != nil {
			return false, fmt.Errorf("%w: %w", types.ErrTarget, err)
		}
	}
	if err := k.register(ctx, adm.Bundle); err != nil {
		return false, err
	}
	key := nativePool()
	before, err := k.poolBalance(ctx, key)
	if err != nil {
		return false, err
	}
	if err := k.debitPool(ctx, key, adm.Amount); err != nil {
		return false, err
	}
	if err := k.burn(ctx, adm.NullifierFees); err != nil {
		return false, err
	}
	kind := pool.KindBond
	if msg.Target == types.UnshieldTargetFeeTopup {
		kind = pool.KindFeeTopup
	}
	waiting, err := k.queueHasEntries(ctx)
	if err != nil {
		return false, err
	}
	if waiting && kind == pool.KindBond {
		// Behind a non-empty queue a bond waits its turn. It has paid nothing, so it takes no cap.
		return true, k.enqueue(ctx, msg, signer, net)
	}
	outcome, err := k.applyCap(ctx, ctx.BlockTime(), key, before, net, kind)
	if err != nil {
		return false, fmt.Errorf("unshield of %s%s: %w", net, params.BaseDenom, err)
	}
	if outcome == pool.OutcomeQueue {
		return true, k.enqueue(ctx, msg, signer, net)
	}
	return false, k.pay(ctx, signer, msg.Target, msg.Validator, msg.NodeId, msg.Role, net)
}

// pay delivers coins from the module account to a target owned by owner.
func (k Keeper) pay(
	ctx sdk.Context, owner sdk.AccAddress, target types.UnshieldTarget,
	validator, nodeID string, role nodestypes.Role, amount math.Int,
) error {
	if target == types.UnshieldTargetFeeTopup {
		if err := k.deps.Fees.CreditFeeBalance(ctx, types.ModuleName, owner, sdk.NewCoin(params.BaseDenom, amount)); err != nil {
			return fmt.Errorf("top up %s's fee balance: %w", owner, err)
		}
		return nil
	}
	if err := k.deps.Bank.SendCoinsFromModuleToAccount(ctx, types.ModuleName, owner, coins(amount)); err != nil {
		return fmt.Errorf("move %s%s to %s: %w", amount, params.BaseDenom, owner, err)
	}
	switch target {
	case types.UnshieldTargetBond:
		return k.deps.Bonder.Delegate(ctx, owner, validator, amount)
	case types.UnshieldTargetNodeBond:
		return k.deps.NodeBonder.BondNode(ctx, &nodestypes.MsgBondNode{
			Operator: owner.String(), NodeId: nodeID, Role: role, Amount: amount,
		})
	default:
		return fmt.Errorf("%w: %s", types.ErrTargetNotLinked, target)
	}
}
