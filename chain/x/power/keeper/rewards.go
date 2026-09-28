package keeper

import (
	"errors"
	"fmt"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/DeBrosOfficial/network/chain/x/power/types"
)

// DistributeEpochRewards pays out totalMint - an epoch's validator/delegator share, already
// minted by x/emission into sourceModule's account - on capped power P_i instead of stock
// x/staking/x/distribution's token-weighted share (plans/open-network/track-c-chain.md C4
// "Rewards are paid on actual power P_i", C3 "It does not use the stock distribution module"). It
// is called from x/emission's BeginBlock (Keeper.closeEpoch) and pays on the lambda already
// stored. RunEndBlock later in the same block is what may raise lambda, after this block's
// staking messages; that new lambda is what the next block's rewards use.
//
// Each validator's share of totalMint is P_i normalized by the sum of every entry's P_i this block
// (Keeper.normalizedShares - security review B5: "rewards must be paid pro rata on
// P_i/sum(P_i)"), not P_i taken directly against a nominal total of 1, which would otherwise let a
// missing chunk of unallocated power (during the ramp, a redistribution-bound shortfall, or a
// zeroed-out ineligible committee member) silently reduce the total actually paid out.
//
// Each validator's amount splits into its own commission (or the whole amount, if it currently has
// no delegators - see distributeValidatorReward) and its delegators' pro-rata shares, all credited
// to earnings accounts (x/fees, via EarningsKeeper) rather than the withdraw-pull model stock
// x/distribution uses - plans/open-network/track-c-chain.md C2 requires rewards land in earnings
// "credited automatically every epoch [with] no withdraw tx". A bootstrap committee member's own
// share is additionally force-bonded (Keeper.forceBondCommitteeReward).
//
// Integer division leaves a small remainder (each validator's share is floored); it is credited to
// the first entry with a positive amount (a deterministic choice - see the loop below), never to an
// entry that received nothing this epoch, mirroring x/emission's own "remainders fold into the
// validator share" rule. DistributeEpochRewards returns the total actually credited, which is
// exactly totalMint whenever at least one entry has positive power.
func (k Keeper) DistributeEpochRewards(ctx sdk.Context, emissionKeeper types.EmissionKeeper, sourceModule string, totalMint math.Int) (math.Int, error) {
	if !totalMint.IsPositive() {
		return math.ZeroInt(), nil
	}

	_, lambda, entries, err := k.computePowers(ctx, emissionKeeper, false)
	if err != nil {
		return math.ZeroInt(), fmt.Errorf("failed to compute powers for epoch reward distribution: %w", err)
	}
	if len(entries) == 0 {
		return math.ZeroInt(), nil
	}
	shares := k.normalizedShares(entries, lambda)

	sumShares := types.SumShares(shares)
	if !sumShares.IsPositive() {
		// Every entry has zero power: RunEndBlock's own empty-validator-set guard would refuse
		// this block too. Fail loudly rather than silently stranding the mint in x/power's module
		// account with nobody to attribute it to.
		return math.ZeroInt(), fmt.Errorf("power: cannot distribute epoch rewards, every validator has zero power this block")
	}

	denom, err := k.stakingKeeper.BondDenom(ctx)
	if err != nil {
		return math.ZeroInt(), fmt.Errorf("failed to read bond denom: %w", err)
	}
	if err := k.bankKeeper.SendCoinsFromModuleToModule(ctx, sourceModule, types.ModuleName, sdk.NewCoins(sdk.NewCoin(denom, totalMint))); err != nil {
		return math.ZeroInt(), fmt.Errorf("failed to pull epoch mint from %s into %s: %w", sourceModule, types.ModuleName, err)
	}

	amounts := make([]math.Int, len(entries))
	distributed := math.ZeroInt()
	remainderRecipient := -1
	for i := range entries {
		amounts[i] = shares[i].MulInt(totalMint).TruncateInt()
		distributed = distributed.Add(amounts[i])
		if remainderRecipient == -1 && amounts[i].IsPositive() {
			remainderRecipient = i
		}
	}
	if remainder := totalMint.Sub(distributed); remainder.IsPositive() {
		if remainderRecipient != -1 {
			amounts[remainderRecipient] = amounts[remainderRecipient].Add(remainder)
			distributed = totalMint
		}
		// else: every entry floored to zero (totalMint smaller than the number of entries); with
		// nowhere positive to fold the dust into, it is simply not minted this epoch - see
		// x/emission's own "an unclaimed ceiling at the end of its epoch is gone."
	} else if remainder.IsNegative() {
		return math.ZeroInt(), fmt.Errorf("power: reward distribution overshot totalMint by %s, this is a bug", remainder.Neg())
	}

	for i, e := range entries {
		if !amounts[i].IsPositive() {
			continue
		}
		if err := k.distributeValidatorReward(ctx, e.operatorAddr, amounts[i], denom); err != nil {
			return math.ZeroInt(), fmt.Errorf("failed to distribute reward to %q: %w", e.operatorAddr, err)
		}
	}

	return distributed, nil
}

// distributeValidatorReward splits amount between validator valoperAddr's own share (commission,
// plus any of its own self-delegation's pro-rata cut) and its delegators' pro-rata shares, crediting
// every recipient's earnings account. If the validator has no real stakingtypes.Validator record
// yet (a bootstrap committee member who has not self-bonded) or has zero delegator shares, the
// entire amount is treated as the operator's own.
func (k Keeper) distributeValidatorReward(ctx sdk.Context, valoperAddr string, amount math.Int, denom string) error {
	valAddr, err := sdk.ValAddressFromBech32(valoperAddr)
	if err != nil {
		return fmt.Errorf("invalid validator operator address %q: %w", valoperAddr, err)
	}
	operatorAcc := sdk.AccAddress(valAddr)

	validator, err := k.stakingKeeper.GetValidator(ctx, valAddr)
	hasValidator := err == nil
	if err != nil && !isValidatorNotFound(err) {
		return fmt.Errorf("failed to load validator %q: %w", valoperAddr, err)
	}

	if !hasValidator || validator.DelegatorShares.IsZero() {
		return k.creditRecipient(ctx, operatorAcc, valoperAddr, amount, denom)
	}

	commission := amount.ToLegacyDec().Mul(validator.Commission.CommissionRates.Rate).TruncateInt()
	shared := amount.Sub(commission)

	delegations, err := k.stakingKeeper.GetValidatorDelegations(ctx, valAddr)
	if err != nil {
		return fmt.Errorf("failed to load delegations for %q: %w", valoperAddr, err)
	}

	distributedShared := math.ZeroInt()
	for _, d := range delegations {
		delAddr, err := sdk.AccAddressFromBech32(d.DelegatorAddress)
		if err != nil {
			return fmt.Errorf("invalid delegator address %q: %w", d.DelegatorAddress, err)
		}
		portion := d.Shares.Quo(validator.DelegatorShares).MulInt(shared).TruncateInt()
		distributedShared = distributedShared.Add(portion)
		if delAddr.Equals(operatorAcc) {
			// The operator's own self-delegation portion is folded into commission below, so it
			// is force-bond-eligible along with the rest of the operator's share, rather than
			// paid out as an ordinary delegator credit.
			commission = commission.Add(portion)
			continue
		}
		if err := k.creditRecipient(ctx, delAddr, "", portion, denom); err != nil {
			return err
		}
	}

	// Any flooring remainder from the delegator split also belongs to the operator.
	commission = commission.Add(shared.Sub(distributedShared))

	if commission.IsPositive() {
		if err := k.creditRecipient(ctx, operatorAcc, valoperAddr, commission, denom); err != nil {
			return err
		}
	}
	return nil
}

// creditRecipient credits amount to addr's earnings account, force-bonding part of it first if
// addr is a bootstrap committee member's own operator account (committeeValoperAddr != "").
func (k Keeper) creditRecipient(ctx sdk.Context, addr sdk.AccAddress, committeeValoperAddr string, amount math.Int, denom string) error {
	if !amount.IsPositive() {
		return nil
	}

	remaining := amount
	if committeeValoperAddr != "" {
		isCommittee, err := k.BootstrapCommittee.Has(ctx, committeeValoperAddr)
		if err != nil {
			return fmt.Errorf("failed to check bootstrap committee membership for %q: %w", committeeValoperAddr, err)
		}
		if isCommittee {
			forceBonded, err := k.forceBondCommitteeReward(ctx, committeeValoperAddr, addr, amount, denom)
			if err != nil {
				return err
			}
			remaining = remaining.Sub(forceBonded)
		}
	}

	if !remaining.IsPositive() {
		return nil
	}
	if err := k.earningsKeeper.CreditEarnings(ctx, types.ModuleName, addr, sdk.NewCoin(denom, remaining)); err != nil {
		return fmt.Errorf("failed to credit earnings for %s: %w", addr, err)
	}
	return nil
}

// forceBondCommitteeReward force-bonds Params.ForceBondFraction of amount into committeeAcc's own
// self-delegation, up to Params.SelfBondCapMultiplier * Params.MinSelfBond total self-bond
// (plans/open-network/track-c-chain.md C4: "50% of each committee member's rewards are
// force-bonded ... until the self-bond reaches 2x the minimum"). It returns the amount actually
// force-bonded (0 if the member has no real validator yet to self-delegate into, the validator is
// currently jailed or has an invalid exchange rate, or the ceiling is already reached), which the
// caller subtracts from what it credits to earnings instead.
//
// Security review C1 ("force-bond halt"): calling stakingKeeper.Delegate on a jailed validator, or
// one whose token/share exchange rate has been driven invalid by a total slash, would either panic
// or return ErrDelegatorShareExRateInvalid and abort the WHOLE epoch's reward distribution (halting
// the chain, since DistributeEpochRewards runs in BeginBlock). Skipping force-bonding for such a
// validator - crediting its member's full share to earnings instead, with no error - keeps the
// chain running through exactly that scenario (see the keeper test that slashes a committee member
// to zero and then closes an epoch).
func (k Keeper) forceBondCommitteeReward(ctx sdk.Context, valoperAddr string, memberAcc sdk.AccAddress, amount math.Int, denom string) (math.Int, error) {
	p, err := k.Params.Get(ctx)
	if err != nil {
		return math.ZeroInt(), fmt.Errorf("failed to load power params: %w", err)
	}

	valAddr, err := sdk.ValAddressFromBech32(valoperAddr)
	if err != nil {
		return math.ZeroInt(), fmt.Errorf("invalid validator operator address %q: %w", valoperAddr, err)
	}
	validator, err := k.stakingKeeper.GetValidator(ctx, valAddr)
	if err != nil {
		if isValidatorNotFound(err) {
			// No real validator to self-delegate into yet: nothing is force-bonded this epoch.
			// The member's first reward, credited to earnings in full, is exactly what lets them
			// self-bond and create one - see the package doc comment on this deliberate gap.
			return math.ZeroInt(), nil
		}
		return math.ZeroInt(), fmt.Errorf("failed to load validator %q: %w", valoperAddr, err)
	}
	if validator.Jailed || validator.InvalidExRate() {
		// Security review C1: never call Delegate on an ineligible validator. The full reward
		// still reaches the member's earnings account via the caller.
		return math.ZeroInt(), nil
	}

	ceiling := p.MinSelfBond.ToLegacyDec().Mul(p.SelfBondCapMultiplier).TruncateInt()
	selfDelegation, err := k.stakingKeeper.GetDelegation(ctx, memberAcc, valAddr)
	selfBonded := math.ZeroInt()
	if err == nil {
		selfBonded = selfDelegation.Shares.Mul(validator.Tokens.ToLegacyDec()).Quo(validator.DelegatorShares).TruncateInt()
	} else if !errors.Is(err, stakingtypes.ErrNoDelegation) {
		return math.ZeroInt(), fmt.Errorf("failed to load self-delegation for %q: %w", valoperAddr, err)
	}
	if selfBonded.GTE(ceiling) {
		return math.ZeroInt(), nil
	}

	wanted := amount.ToLegacyDec().Mul(p.ForceBondFraction).TruncateInt()
	room := ceiling.Sub(selfBonded)
	if wanted.GT(room) {
		wanted = room
	}
	if !wanted.IsPositive() {
		return math.ZeroInt(), nil
	}

	if err := k.bankKeeper.SendCoinsFromModuleToAccount(ctx, types.ModuleName, memberAcc, sdk.NewCoins(sdk.NewCoin(denom, wanted))); err != nil {
		return math.ZeroInt(), fmt.Errorf("failed to fund %s for force-bonding: %w", memberAcc, err)
	}
	if _, err := k.stakingKeeper.Delegate(ctx, memberAcc, wanted, stakingtypes.Unbonded, validator, true); err != nil {
		return math.ZeroInt(), fmt.Errorf("failed to force-bond %s into %s: %w", wanted, valoperAddr, err)
	}

	total, err := k.CommitteeSelfBond.Get(ctx, valoperAddr)
	if err != nil {
		if !isNotFound(err) {
			return math.ZeroInt(), fmt.Errorf("failed to load committee self-bond total for %q: %w", valoperAddr, err)
		}
		total = math.ZeroInt()
	}
	if err := k.CommitteeSelfBond.Set(ctx, valoperAddr, total.Add(wanted)); err != nil {
		return math.ZeroInt(), fmt.Errorf("failed to record committee self-bond total for %q: %w", valoperAddr, err)
	}

	return wanted, nil
}
