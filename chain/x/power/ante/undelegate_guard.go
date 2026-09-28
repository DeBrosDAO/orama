// Package ante implements x/power's ante decorators: guards over ordinary x/staking messages that
// x/power's bootstrap committee mechanics need but that x/staking itself has no reason to enforce
// on its own (plans/open-network/track-c-chain.md C4).
package ante

import (
	"context"
	"errors"
	"fmt"

	errorsmod "cosmossdk.io/errors"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/power/keeper"
)

// StakingKeeper is the subset of x/staking's keeper UndelegateGuard needs to compute a committee
// member's current self-bond.
type StakingKeeper interface {
	GetValidator(ctx context.Context, addr sdk.ValAddress) (stakingtypes.Validator, error)
	GetDelegation(ctx context.Context, delAddr sdk.AccAddress, valAddr sdk.ValAddress) (stakingtypes.Delegation, error)
}

// UndelegateGuard rejects a bootstrap committee member's own MsgUndelegate/MsgBeginRedelegate
// (moving stake OUT of its own self-delegation) whenever doing so would take that self-bond below
// the amount x/power has force-bonded into it so far (Keeper.CommitteeSelfBond), while the hand-over
// is still in progress (lambda < 1) - security review B1/M4: "lock the force-bonded stake while the
// seat is held". Force-bonding only has teeth as a deterrent against double-signing if the stake it
// puts at risk cannot simply be withdrawn again before the committee's job is done. Once lambda
// reaches 1, the committee's bootstrap seats have already lapsed (see types.ComputePower's doc
// comment), so the lock is no longer needed and this decorator becomes a no-op for every message.
//
// This never touches an outsider's or an ordinary delegator's undelegation, and never touches a
// committee member's undelegation from a THIRD PARTY's validator - only its own self-delegation.
type UndelegateGuard struct {
	stakingKeeper StakingKeeper
	powerKeeper   keeper.Keeper
}

// NewUndelegateGuard builds an UndelegateGuard.
func NewUndelegateGuard(stakingKeeper StakingKeeper, powerKeeper keeper.Keeper) UndelegateGuard {
	return UndelegateGuard{stakingKeeper: stakingKeeper, powerKeeper: powerKeeper}
}

func (d UndelegateGuard) AnteHandle(ctx sdk.Context, tx sdk.Tx, simulate bool, next sdk.AnteHandler) (sdk.Context, error) {
	for _, msg := range tx.GetMsgs() {
		var delAddrStr, valAddrStr string
		var amount math.Int
		switch m := msg.(type) {
		case *stakingtypes.MsgUndelegate:
			delAddrStr, valAddrStr, amount = m.DelegatorAddress, m.ValidatorAddress, m.Amount.Amount
		case *stakingtypes.MsgBeginRedelegate:
			delAddrStr, valAddrStr, amount = m.DelegatorAddress, m.ValidatorSrcAddress, m.Amount.Amount
		default:
			continue
		}
		if err := d.checkWithdrawal(ctx, delAddrStr, valAddrStr, amount); err != nil {
			return ctx, err
		}
	}
	return next(ctx, tx, simulate)
}

// checkWithdrawal returns an error if withdrawing amount from delAddrStr's delegation to
// valAddrStr would take a committee member's own self-bond below its locked force-bonded floor.
func (d UndelegateGuard) checkWithdrawal(ctx sdk.Context, delAddrStr, valAddrStr string, amount math.Int) error {
	delAddr, err := sdk.AccAddressFromBech32(delAddrStr)
	if err != nil {
		return errorsmod.Wrap(sdkerrors.ErrInvalidAddress, err.Error())
	}
	valAddr, err := sdk.ValAddressFromBech32(valAddrStr)
	if err != nil {
		return errorsmod.Wrap(sdkerrors.ErrInvalidAddress, err.Error())
	}
	// Only a SELF-delegation (the validator's own operator account withdrawing from its own
	// validator) can ever be force-bonded stake; an outsider's or a third party's delegation to a
	// committee member's validator is never touched by force-bonding at all.
	if !sdk.AccAddress(valAddr).Equals(delAddr) {
		return nil
	}

	isCommittee, err := d.powerKeeper.BootstrapCommittee.Has(ctx, valAddrStr)
	if err != nil {
		return fmt.Errorf("failed to check bootstrap committee membership for %q: %w", valAddrStr, err)
	}
	if !isCommittee {
		return nil
	}

	lambda, err := d.powerKeeper.Lambda.Get(ctx)
	if err != nil {
		return fmt.Errorf("failed to load lambda: %w", err)
	}
	if lambda.GTE(math.LegacyOneDec()) {
		// The hand-over is complete and this seat has already lapsed: no more lock.
		return nil
	}

	locked, err := d.powerKeeper.CommitteeSelfBond.Get(ctx, valAddrStr)
	if err != nil {
		if !errors.Is(err, collections.ErrNotFound) {
			return fmt.Errorf("failed to load committee self-bond for %q: %w", valAddrStr, err)
		}
		locked = math.ZeroInt()
	}
	if !locked.IsPositive() {
		return nil
	}

	validator, err := d.stakingKeeper.GetValidator(ctx, valAddr)
	if err != nil {
		return fmt.Errorf("failed to load validator %q: %w", valAddrStr, err)
	}
	delegation, err := d.stakingKeeper.GetDelegation(ctx, delAddr, valAddr)
	if err != nil {
		return fmt.Errorf("failed to load self-delegation for %q: %w", valAddrStr, err)
	}
	if validator.DelegatorShares.IsZero() {
		return nil
	}
	currentSelfBond := delegation.Shares.MulInt(validator.Tokens).Quo(validator.DelegatorShares).TruncateInt()
	resulting := currentSelfBond.Sub(amount)
	if resulting.LT(locked) {
		return errorsmod.Wrapf(
			sdkerrors.ErrInvalidRequest,
			"cannot withdraw %s%s from %s's self-bond: %s%s is force-bonded and locked until lambda reaches 1 (currently %s)",
			amount, params.BaseDenom, valAddrStr, locked, params.BaseDenom, lambda,
		)
	}
	return nil
}
