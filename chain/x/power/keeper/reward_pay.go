package keeper

import (
	"fmt"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/power/types"
)

// rewardSplit is how one validator's epoch reward was paid. operator + delegators == amount;
// commission is the part of operator that is the validator's commission rate (the rest is the
// operator's own pro-rata cut and the flooring remainder); forceBonded is the part of operator
// bonded into a committee member's self-delegation instead of credited to earnings.
type rewardSplit struct {
	validator   string
	amount      math.Int
	commission  math.Int
	operator    math.Int
	delegators  math.Int
	forceBonded math.Int
}

func emitValidatorReward(ctx sdk.Context, s rewardSplit) {
	ctx.EventManager().EmitEvent(sdk.NewEvent(types.EventTypeValidatorReward,
		sdk.NewAttribute(types.AttributeKeyValidator, s.validator),
		sdk.NewAttribute(types.AttributeKeyAmount, s.amount.String()),
		sdk.NewAttribute(types.AttributeKeyCommission, s.commission.String()),
		sdk.NewAttribute(types.AttributeKeyOperatorShare, s.operator.String()),
		sdk.NewAttribute(types.AttributeKeyDelegators, s.delegators.String()),
		sdk.NewAttribute(types.AttributeKeyForceBonded, s.forceBonded.String()),
	))
}

func emitDelegatorReward(ctx sdk.Context, validator string, delegator sdk.AccAddress, amount math.Int) {
	if !amount.IsPositive() {
		return
	}
	ctx.EventManager().EmitEvent(sdk.NewEvent(types.EventTypeDelegatorReward,
		sdk.NewAttribute(types.AttributeKeyValidator, validator),
		sdk.NewAttribute(types.AttributeKeyDelegator, delegator.String()),
		sdk.NewAttribute(types.AttributeKeyAmount, amount.String()),
	))
}

// distributeValidatorReward splits amount between validator valoperAddr's own share (commission,
// plus any of its own self-delegation's pro-rata cut) and its delegators' pro-rata shares, crediting
// every recipient's earnings account. If the validator has no real stakingtypes.Validator record
// yet (a bootstrap committee member who has not self-bonded) or has zero delegator shares, the
// entire amount is treated as the operator's own. It emits a power_delegator_reward event for each
// delegator other than the operator and one power_validator_reward event for the validator
// (emitValidatorReward, emitDelegatorReward), so an indexer can attribute the epoch's payout without reading state.
func (k Keeper) distributeValidatorReward(ctx sdk.Context, valoperAddr string, amount math.Int, denom string) error {
	valAddr, err := sdk.ValAddressFromBech32(valoperAddr)
	if err != nil {
		return rejectReward(fmt.Errorf("invalid validator operator address %q: %w", valoperAddr, err))
	}
	operatorAcc := sdk.AccAddress(valAddr)

	validator, err := k.stakingKeeper.GetValidator(ctx, valAddr)
	hasValidator := err == nil
	if err != nil && !isValidatorNotFound(err) {
		return rejectReward(fmt.Errorf("failed to load validator %q: %w", valoperAddr, err))
	}

	split := rewardSplit{validator: valoperAddr, amount: amount, commission: math.ZeroInt(), operator: amount, delegators: math.ZeroInt(), forceBonded: math.ZeroInt()}
	if !hasValidator || validator.DelegatorShares.IsZero() {
		split.forceBonded, err = k.creditRecipient(ctx, operatorAcc, valoperAddr, amount, denom)
		if err != nil {
			return err
		}
		emitValidatorReward(ctx, split)
		return nil
	}

	split.commission = amount.ToLegacyDec().Mul(validator.Commission.CommissionRates.Rate).TruncateInt()
	shared := amount.Sub(split.commission)

	delegations, err := k.stakingKeeper.GetValidatorDelegations(ctx, valAddr)
	if err != nil {
		return rejectReward(fmt.Errorf("failed to load delegations for %q: %w", valoperAddr, err))
	}

	for _, d := range delegations {
		delAddr, err := sdk.AccAddressFromBech32(d.DelegatorAddress)
		if err != nil {
			return rejectReward(fmt.Errorf("invalid delegator address %q: %w", d.DelegatorAddress, err))
		}
		portion := d.Shares.Quo(validator.DelegatorShares).MulInt(shared).TruncateInt()
		if delAddr.Equals(operatorAcc) {
			// The operator's own self-delegation portion is part of the operator's share below, so
			// it is force-bond-eligible with the rest of it, rather than paid out as an ordinary
			// delegator credit.
			continue
		}
		split.delegators = split.delegators.Add(portion)
		if _, err := k.creditRecipient(ctx, delAddr, "", portion, denom); err != nil {
			return err
		}
		emitDelegatorReward(ctx, valoperAddr, delAddr, portion)
	}

	// Any flooring remainder from the delegator split also belongs to the operator.
	split.operator = amount.Sub(split.delegators)
	if split.operator.IsPositive() {
		split.forceBonded, err = k.creditRecipient(ctx, operatorAcc, valoperAddr, split.operator, denom)
		if err != nil {
			return err
		}
	}
	emitValidatorReward(ctx, split)
	return nil
}

// creditRecipient credits amount to addr's earnings account, force-bonding part of it first if
// addr is a bootstrap committee member's own operator account (committeeValoperAddr != ""). It
// returns the amount it force-bonded instead of crediting.
func (k Keeper) creditRecipient(ctx sdk.Context, addr sdk.AccAddress, committeeValoperAddr string, amount math.Int, denom string) (math.Int, error) {
	forceBonded := math.ZeroInt()
	if !amount.IsPositive() {
		return forceBonded, nil
	}

	remaining := amount
	if committeeValoperAddr != "" {
		isCommittee, err := k.BootstrapCommittee.Has(ctx, committeeValoperAddr)
		if err != nil {
			return forceBonded, fmt.Errorf("failed to check bootstrap committee membership for %q: %w", committeeValoperAddr, err)
		}
		if isCommittee {
			forceBonded, err = k.forceBondCommitteeReward(ctx, committeeValoperAddr, addr, amount, denom)
			if err != nil {
				return math.ZeroInt(), err
			}
			remaining = remaining.Sub(forceBonded)
		}
	}

	if !remaining.IsPositive() {
		return forceBonded, nil
	}
	if err := k.earningsKeeper.CreditEarnings(ctx, types.ModuleName, addr, sdk.NewCoin(denom, remaining)); err != nil {
		return math.ZeroInt(), rejectReward(fmt.Errorf("failed to credit earnings for %s: %w", addr, err))
	}
	return forceBonded, nil
}
