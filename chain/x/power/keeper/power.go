package keeper

import (
	"errors"
	"fmt"

	abci "github.com/cometbft/cometbft/abci/types"
	cmtprotocrypto "github.com/cometbft/cometbft/proto/tendermint/crypto"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"

	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/DeBrosOfficial/network/chain/x/power/types"
)

// isNotFound reports whether err is a collections "key not found" error.
func isNotFound(err error) bool {
	return errors.Is(err, collections.ErrNotFound)
}

// isValidatorNotFound reports whether err is x/staking's "validator does not exist" sentinel.
func isValidatorNotFound(err error) bool {
	return errors.Is(err, stakingtypes.ErrNoValidatorFound)
}

// validatorEntry is one validator in x/power's per-block universe: every bootstrap committee
// member (whether or not it is currently eligible - see committeeEligibility) plus every currently
// bonded outsider validator.
type validatorEntry struct {
	operatorAddr   string
	pubKeyBytes    []byte
	bootstrap      math.LegacyDec
	rampedCapped   math.LegacyDec
	unrampedCapped math.LegacyDec
}

// computePowers loads lambda and the cap and builds this block's validator entries.
// advance is true only for RunEndBlock. That call is the one that may raise lambda,
// and it runs after the block's staking messages, so the rate limit sees the stake
// that this block will actually publish. DistributeEpochRewards passes false and
// pays on the lambda stored by the previous block.
func (k Keeper) computePowers(ctx sdk.Context, emissionKeeper types.EmissionKeeper, advance bool) (types.Params, math.LegacyDec, []validatorEntry, error) {
	p, err := k.Params.Get(ctx)
	if err != nil {
		return types.Params{}, math.LegacyDec{}, nil, fmt.Errorf("failed to load power params: %w", err)
	}

	currentEpoch, err := emissionKeeper.CurrentEpoch(ctx)
	if err != nil {
		return types.Params{}, math.LegacyDec{}, nil, fmt.Errorf("failed to read current emission epoch: %w", err)
	}

	var committee []types.BootstrapMember
	if err := k.BootstrapCommittee.Walk(ctx, nil, func(_ string, m types.BootstrapMember) (bool, error) {
		committee = append(committee, m)
		return false, nil
	}); err != nil {
		return types.Params{}, math.LegacyDec{}, nil, fmt.Errorf("failed to walk bootstrap committee: %w", err)
	}

	eligible := make(map[string]bool, len(committee))
	for _, m := range committee {
		valAddrStr, err := valoperFromAccountBech32(m.OperatorAddress)
		if err != nil {
			return types.Params{}, math.LegacyDec{}, nil, fmt.Errorf("corrupted bootstrap committee operator_address %q: %w", m.OperatorAddress, err)
		}
		ok, err := k.committeeEligible(ctx, valAddrStr)
		if err != nil {
			return types.Params{}, math.LegacyDec{}, nil, err
		}
		eligible[valAddrStr] = ok
	}

	bonded, err := k.stakingKeeper.GetBondedValidatorsByPower(ctx)
	if err != nil {
		return types.Params{}, math.LegacyDec{}, nil, fmt.Errorf("failed to load bonded validators: %w", err)
	}

	// activeOperatorCount (security review, non-blocking "UpdateCapState should count committee
	// members") is the number of DISTINCT operators actually participating in consensus this
	// block: the operator of every bonded (stake-indexed) validator, plus the operator of every
	// currently eligible committee member not already counted among them (a committee member who
	// has self-bonded enough to appear in the bonded-by-power index is not double-counted). The
	// cap, its hysteresis and the hand-over gate all measure independent parties, so a party that
	// runs many validators counts once.
	activeSet := make(map[string][]byte, len(bonded)+len(committee))
	for _, v := range bonded {
		pk, err := consPubKeyBytes(v)
		if err != nil {
			return types.Params{}, math.LegacyDec{}, nil, fmt.Errorf("failed to read consensus pubkey for %q: %w", v.OperatorAddress, err)
		}
		activeSet[v.OperatorAddress] = pk
	}
	for _, m := range committee {
		valAddrStr, err := valoperFromAccountBech32(m.OperatorAddress)
		if err != nil {
			return types.Params{}, math.LegacyDec{}, nil, fmt.Errorf("corrupted bootstrap committee operator_address %q: %w", m.OperatorAddress, err)
		}
		if _, counted := activeSet[valAddrStr]; counted || !eligible[valAddrStr] {
			continue
		}
		activeSet[valAddrStr] = m.ConsensusPubkey
	}
	activeOperators := make([]string, 0, len(activeSet))
	for addr, pk := range activeSet {
		operator, err := k.operatorOf(ctx, addr, pk)
		if err != nil {
			return types.Params{}, math.LegacyDec{}, nil, err
		}
		activeOperators = append(activeOperators, operator)
	}
	activeValidatorCount := types.CountOperators(activeOperators)

	lambda, err := k.Lambda.Get(ctx)
	if err != nil {
		return types.Params{}, math.LegacyDec{}, nil, fmt.Errorf("failed to load lambda: %w", err)
	}
	capBps, err := k.CapCurrentBps.Get(ctx)
	if err != nil {
		return types.Params{}, math.LegacyDec{}, nil, fmt.Errorf("failed to load current_cap_bps: %w", err)
	}
	var prevLambda math.LegacyDec
	advanced := false
	capFraction := types.CapFractionBps(capBps)
	if advance {
		lambda, capFraction, prevLambda, advanced, err = k.advanceEpochIfNeeded(ctx, p, currentEpoch, activeValidatorCount)
		if err != nil {
			return types.Params{}, math.LegacyDec{}, nil, err
		}
	}

	stakes, pubKeyByAddr, err := k.powerStakes(ctx, bonded)
	if err != nil {
		return types.Params{}, math.LegacyDec{}, nil, err
	}
	// Full-token shares are the latent stake the lambda clamp measures.
	// Published shares use only the tokens whose ramp has admitted them, so a
	// bond added after the ramp finished cannot move voting power in that block.
	// The cap binds per operator: the shares of one operator's validators sum to at most the cap.
	tagged, err := k.operatorStakes(ctx, stakes, pubKeyByAddr)
	if err != nil {
		return types.Params{}, math.LegacyDec{}, nil, err
	}
	fullShares := types.ComputeOperatorCappedShares(tagged, capFraction, p.MaxRedistributionMultiplier)
	effective := make([]types.OperatorStake, len(stakes))
	effectiveTotal := math.ZeroInt()
	for i, s := range tagged {
		tokens, err := k.effectiveBond(ctx, s.OperatorAddress, s.BondedTokens, currentEpoch, p.RampEpochs, advance)
		if err != nil {
			return types.Params{}, math.LegacyDec{}, nil, err
		}
		effective[i] = types.OperatorStake{OperatorAddress: s.OperatorAddress, Operator: s.Operator, BondedTokens: tokens}
		effectiveTotal = effectiveTotal.Add(tokens)
	}
	// Validators with nothing admitted yet stay at C_i = 0. Passing them into
	// ComputeCappedShares would still give them an equal share whenever the cap
	// cannot bind (cap * n < 1), which is every set smaller than 20 validators
	// at the 5% cap.
	admittedByAddr := make(map[string]math.LegacyDec, len(stakes))
	if effectiveTotal.IsPositive() {
		positive := make([]types.OperatorStake, 0, len(effective))
		for _, s := range effective {
			if s.BondedTokens.IsPositive() {
				positive = append(positive, s)
			}
		}
		computed := types.ComputeOperatorCappedShares(positive, capFraction, p.MaxRedistributionMultiplier)
		for i, s := range positive {
			admittedByAddr[s.OperatorAddress] = computed[i]
		}
	}

	stakeAddrs := make(map[string]bool, len(stakes))
	rampedByAddr := make(map[string]math.LegacyDec, len(stakes))
	unrampedByAddr := make(map[string]math.LegacyDec, len(stakes))
	for i, s := range stakes {
		stakeAddrs[s.OperatorAddress] = true
		unrampedByAddr[s.OperatorAddress] = fullShares[i]
		if !fullShares[i].IsPositive() {
			// A share that falls back to zero while the validator is still bonded
			// must not keep a ramp clock or admitted tokens. The next positive
			// balance starts over.
			if err := k.clearRampActivation(ctx, s.OperatorAddress); err != nil {
				return types.Params{}, math.LegacyDec{}, nil, err
			}
			if err := k.clearStakeRamp(ctx, s.OperatorAddress); err != nil {
				return types.Params{}, math.LegacyDec{}, nil, err
			}
			rampedByAddr[s.OperatorAddress] = math.LegacyZeroDec()
			continue
		}
		if err := k.stampRampActivationIfNeeded(ctx, s.OperatorAddress, fullShares[i], currentEpoch); err != nil {
			return types.Params{}, math.LegacyDec{}, nil, err
		}
		share, ok := admittedByAddr[s.OperatorAddress]
		if !ok {
			share = math.LegacyZeroDec()
		}
		rampedByAddr[s.OperatorAddress] = share
	}
	if err := k.pruneStaleRampActivations(ctx, stakeAddrs); err != nil {
		return types.Params{}, math.LegacyDec{}, nil, err
	}

	entries, err := k.buildValidatorEntries(ctx, committee, eligible, rampedByAddr, unrampedByAddr, pubKeyByAddr)
	if err != nil {
		return types.Params{}, math.LegacyDec{}, nil, err
	}
	if advanced {
		lambda, err = k.limitLambdaStep(ctx, prevLambda, lambda, entries)
		if err != nil {
			return types.Params{}, math.LegacyDec{}, nil, err
		}
	}

	return p, lambda, entries, nil
}

// committeeEligible reports whether the committee member at valAddrStr currently backs its seat
// with a real staking record in good standing (security review B1/H2): the record must exist, be
// Bonded, not jailed, and its consensus address must not be tombstoned. A missing record (never
// self-bonded... though InitGenesis always creates one - see genesis.go), an unbonding/unbonded
// record, a jailed one, or a tombstoned one all mean the seat currently carries zero bootstrap
// power. A tombstone can never revert (x/slashing never un-tombstones), so this permanently retires
// the seat; a stakeless member that gets jailed has no self-bond to unjail with, so it stays at
// zero power forever too - both are accepted, documented outcomes (see docs/whitepaper/technical-reference/appendices/e-chain-messages-and-queries.md).
func (k Keeper) committeeEligible(ctx sdk.Context, valAddrStr string) (bool, error) {
	valAddr, err := sdk.ValAddressFromBech32(valAddrStr)
	if err != nil {
		return false, fmt.Errorf("invalid valoper address %q: %w", valAddrStr, err)
	}
	validator, err := k.stakingKeeper.GetValidator(ctx, valAddr)
	if err != nil {
		if isValidatorNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to load validator %q: %w", valAddrStr, err)
	}
	if validator.Jailed || validator.Status != stakingtypes.Bonded {
		return false, nil
	}
	consAddr, err := validator.GetConsAddr()
	if err != nil {
		return false, fmt.Errorf("failed to derive consensus address for %q: %w", valAddrStr, err)
	}
	if k.slashingKeeper.IsTombstoned(ctx, consAddr) {
		return false, nil
	}
	return true, nil
}

// RunEndBlock is x/power's ABCI EndBlock (plans/open-network/track-c-chain.md C4): it advances
// lambda and the cap once per closed x/emission epoch, recomputes every validator's power for
// this block, and returns the CometBFT ValidatorUpdates needed to move from the previous block's
// assignment to this one. It is the ONLY source of ValidatorUpdates this app returns to CometBFT
// (see app.stakingEndBlockOverride, which discards x/staking's own).
func (k Keeper) RunEndBlock(ctx sdk.Context, emissionKeeper types.EmissionKeeper) ([]abci.ValidatorUpdate, error) {
	p, lambda, entries, err := k.computePowers(ctx, emissionKeeper, true)
	if err != nil {
		return nil, err
	}

	shares, err := k.limitPublishedIncreases(ctx, entries, k.normalizedShares(entries, lambda))
	if err != nil {
		return nil, err
	}

	updates := make([]abci.ValidatorUpdate, 0)
	seen := make(map[string]bool, len(entries))
	survivingPower := 0
	for i, e := range entries {
		seen[e.operatorAddr] = true
		cometPower := types.PowerToCometBFT(shares[i], p.CometPowerScale)
		if cometPower > 0 {
			survivingPower++
		}

		last, hasLast, err := k.lastPower(ctx, e.operatorAddr)
		if err != nil {
			return nil, err
		}
		if cometPower == last && hasLast {
			continue
		}
		if cometPower == 0 && !hasLast {
			// Never had power, still has none: nothing to report.
			continue
		}

		update, err := k.recordAndBuildUpdate(ctx, e.operatorAddr, e.pubKeyBytes, cometPower)
		if err != nil {
			return nil, err
		}
		updates = append(updates, update)
	}

	// Security review L5/"never send an empty validator set": if this block's computation would
	// leave NO validator with positive power at all, that is a fatal misconfiguration (or a bug),
	// not a state to hand to CometBFT - an empty validator set halts the chain far more
	// destructively than refusing the block here does.
	if survivingPower == 0 {
		return nil, fmt.Errorf("power: this block's validator set would be empty (0 validators with positive power); refusing to return an empty CometBFT validator set")
	}

	// Anything that had power before but is no longer in this block's universe at all (e.g. an
	// outsider that fully unbonded) must be zeroed out too.
	removalUpdates, err := k.removeDroppedValidators(ctx, seen)
	if err != nil {
		return nil, err
	}
	updates = append(updates, removalUpdates...)

	return updates, nil
}

// normalizedShares divides each entry's raw P_i (types.ComputePower) by the sum across every entry
// this block (security review B5/M5: "rewards must be paid pro rata on P_i/sum(P_i)" and "no fully-
// ramped validator may exceed the cap in real voting power"). Raw P_i can legitimately sum to less
// than 1 - during the ramp, while ComputeCappedShares' redistribution bound leaves capacity
// unallocated (see its doc comment), or while ineligible committee members are zeroed out - so
// CometBFT power and reward shares are always computed against the REAL total actually assigned,
// never against a nominal total of 1. If every entry has zero power (guarded by the empty-set check
// in RunEndBlock and the zero-sum check in DistributeEpochRewards), this returns all zeros rather
// than dividing by zero.
func (k Keeper) normalizedShares(entries []validatorEntry, lambda math.LegacyDec) []math.LegacyDec {
	raw := make([]math.LegacyDec, len(entries))
	sum := math.LegacyZeroDec()
	for i, e := range entries {
		raw[i] = types.ComputePower(e.bootstrap, e.rampedCapped, lambda)
		sum = sum.Add(raw[i])
	}
	shares := make([]math.LegacyDec, len(entries))
	if !sum.IsPositive() {
		for i := range shares {
			shares[i] = math.LegacyZeroDec()
		}
		return shares
	}
	for i := range raw {
		shares[i] = raw[i].Quo(sum)
	}
	return shares
}

// advanceEpochIfNeeded recomputes lambda and the cap exactly once per closed epoch
// (plans/open-network/track-c-chain.md C4: both are "recomputed each epoch"), and returns the
// values to use for this block either way (the just-recomputed ones, or last epoch's if this
// block isn't a fresh epoch).
func (k Keeper) advanceEpochIfNeeded(ctx sdk.Context, p types.Params, currentEpoch, activeValidatorCount uint64) (lambda, capFraction, prevLambda math.LegacyDec, advanced bool, err error) {
	lastUpdated, err := k.LambdaLastUpdatedEpoch.Get(ctx)
	if err != nil {
		return math.LegacyDec{}, math.LegacyDec{}, math.LegacyDec{}, false, fmt.Errorf("failed to load lambda_last_updated_epoch: %w", err)
	}
	lambda, err = k.Lambda.Get(ctx)
	if err != nil {
		return math.LegacyDec{}, math.LegacyDec{}, math.LegacyDec{}, false, fmt.Errorf("failed to load lambda: %w", err)
	}
	capBps, err := k.CapCurrentBps.Get(ctx)
	if err != nil {
		return math.LegacyDec{}, math.LegacyDec{}, math.LegacyDec{}, false, fmt.Errorf("failed to load current_cap_bps: %w", err)
	}

	if currentEpoch <= lastUpdated {
		return lambda, types.CapFractionBps(capBps), lambda, false, nil
	}

	// Cap state first: the handover gate's threshold (below) is measured against whichever cap
	// fraction is in force for the epoch being closed.
	streak, err := k.CapBelowStreak.Get(ctx)
	if err != nil {
		return math.LegacyDec{}, math.LegacyDec{}, math.LegacyDec{}, false, fmt.Errorf("failed to load cap streak: %w", err)
	}
	newCap := types.UpdateCapState(types.CapState{CurrentCapBps: capBps, BelowStepUpStreakEpochs: streak}, activeValidatorCount, p)
	if err := k.CapCurrentBps.Set(ctx, newCap.CurrentCapBps); err != nil {
		return math.LegacyDec{}, math.LegacyDec{}, math.LegacyDec{}, false, fmt.Errorf("failed to set current_cap_bps: %w", err)
	}
	if err := k.CapBelowStreak.Set(ctx, newCap.BelowStepUpStreakEpochs); err != nil {
		return math.LegacyDec{}, math.LegacyDec{}, math.LegacyDec{}, false, fmt.Errorf("failed to set cap streak: %w", err)
	}
	capFraction = types.CapFractionBps(newCap.CurrentCapBps)

	genesisEpoch, err := k.GenesisEpoch.Get(ctx)
	if err != nil {
		return math.LegacyDec{}, math.LegacyDec{}, math.LegacyDec{}, false, fmt.Errorf("failed to load genesis_epoch: %w", err)
	}
	var epochsSinceGenesis uint64
	if currentEpoch > genesisEpoch {
		epochsSinceGenesis = currentEpoch - genesisEpoch
	}

	bondedTotal := math.ZeroInt()
	allBonded, err := k.stakingKeeper.GetBondedValidatorsByPower(ctx)
	if err != nil {
		return math.LegacyDec{}, math.LegacyDec{}, math.LegacyDec{}, false, fmt.Errorf("failed to load bonded validators for lambda: %w", err)
	}
	for _, v := range allBonded {
		bondedTotal = bondedTotal.Add(v.Tokens)
	}

	prevLambda = lambda
	newLambda := types.ComputeLambda(prevLambda, bondedTotal, p.BootstrapExitStake, epochsSinceGenesis, p.BootstrapDeadlineEpochs)

	// Security review H3(a): hold lambda at Params.PreGateLambdaCap until enough consensus
	// participants have been active at once (types.HandoverGateThreshold). That count includes
	// eligible committee members with no stake. The flag is a one-way ratchet, so a chain that
	// once clears the gate never has it re-imposed even if the active count later drops. Without
	// this, a Sybil-cheap validator set could take over full consensus power the moment the
	// bootstrap deadline elapses, regardless of how little real stake backs it.
	gateSatisfied, err := k.GateSatisfied.Get(ctx)
	if err != nil {
		return math.LegacyDec{}, math.LegacyDec{}, math.LegacyDec{}, false, fmt.Errorf("failed to load gate_satisfied: %w", err)
	}
	if !gateSatisfied && activeValidatorCount >= types.HandoverGateThreshold(capFraction) {
		gateSatisfied = true
		if err := k.GateSatisfied.Set(ctx, true); err != nil {
			return math.LegacyDec{}, math.LegacyDec{}, math.LegacyDec{}, false, fmt.Errorf("failed to set gate_satisfied: %w", err)
		}
	}
	if !gateSatisfied && newLambda.GT(p.PreGateLambdaCap) {
		newLambda = p.PreGateLambdaCap
	}

	if err := k.Lambda.Set(ctx, newLambda); err != nil {
		return math.LegacyDec{}, math.LegacyDec{}, math.LegacyDec{}, false, fmt.Errorf("failed to set lambda: %w", err)
	}
	if err := k.LambdaLastUpdatedEpoch.Set(ctx, currentEpoch); err != nil {
		return math.LegacyDec{}, math.LegacyDec{}, math.LegacyDec{}, false, fmt.Errorf("failed to set lambda_last_updated_epoch: %w", err)
	}

	k.Logger(ctx).Info(
		"power epoch advanced",
		"epoch", currentEpoch, "lambda", newLambda.String(), "cap_bps", newCap.CurrentCapBps,
		"active_validator_count", activeValidatorCount, "gate_satisfied", gateSatisfied,
	)

	return newLambda, capFraction, prevLambda, true, nil
}

// clearRampActivation drops a ramp clock whose capped share is no longer positive.
func (k Keeper) clearRampActivation(ctx sdk.Context, operatorAddr string) error {
	has, err := k.RampActivation.Has(ctx, operatorAddr)
	if err != nil {
		return fmt.Errorf("failed to check ramp activation for %q: %w", operatorAddr, err)
	}
	if !has {
		return nil
	}
	if err := k.RampActivation.Remove(ctx, operatorAddr); err != nil {
		return fmt.Errorf("failed to reset ramp activation for %q: %w", operatorAddr, err)
	}
	return nil
}

// stampRampActivationIfNeeded records the current epoch as a validator's ramp start the first time
// its capped share is observed to be positive. A non-positive share clears any existing clock
// (security review H3(c)) so a later return to a positive share starts the ramp over.
func (k Keeper) stampRampActivationIfNeeded(ctx sdk.Context, operatorAddr string, cappedShare math.LegacyDec, currentEpoch uint64) error {
	if !cappedShare.IsPositive() {
		return k.clearRampActivation(ctx, operatorAddr)
	}
	has, err := k.RampActivation.Has(ctx, operatorAddr)
	if err != nil {
		return fmt.Errorf("failed to check ramp activation for %q: %w", operatorAddr, err)
	}
	if has {
		return nil
	}
	if err := k.RampActivation.Set(ctx, operatorAddr, currentEpoch); err != nil {
		return fmt.Errorf("failed to stamp ramp activation for %q: %w", operatorAddr, err)
	}
	return nil
}

// pruneStaleRampActivations removes every RampActivation entry whose operator address is not in
// stakeAddrs (security review, non-blocking "ramp bypass": a validator that fully unbonds or gets
// jailed drops out of the bonded-by-power set; without pruning, a later re-bond would keep its OLD
// activation epoch and skip the ramp entirely). A future re-activation then starts the ramp fresh.
func (k Keeper) pruneStaleRampActivations(ctx sdk.Context, stakeAddrs map[string]bool) error {
	var stale []string
	if err := k.RampActivation.Walk(ctx, nil, func(addr string, _ uint64) (bool, error) {
		if !stakeAddrs[addr] {
			stale = append(stale, addr)
		}
		return false, nil
	}); err != nil {
		return fmt.Errorf("failed to walk ramp activations: %w", err)
	}
	for _, addr := range stale {
		if err := k.RampActivation.Remove(ctx, addr); err != nil {
			return fmt.Errorf("failed to prune ramp activation for %q: %w", addr, err)
		}
		if err := k.clearStakeRamp(ctx, addr); err != nil {
			return err
		}
	}
	return nil
}

// buildValidatorEntries assembles this block's full validator universe: every bootstrap committee
// member (their B_i is EqualBootstrapShares(len(committee)) when currently eligible - see
// committeeEligible - and 0 otherwise, regardless of stake) plus every bonded outsider with a
// positive ramped capped share not already counted as a committee member. Iteration is over
// committee (already collected in a fixed order by computePowers) and the pre-sorted
// stakes/rampedByAddr built there (already in x/staking's own deterministic order), so the result
// is independent of any Go map iteration order.
func (k Keeper) buildValidatorEntries(
	ctx sdk.Context,
	committee []types.BootstrapMember,
	eligible map[string]bool,
	rampedByAddr map[string]math.LegacyDec,
	unrampedByAddr map[string]math.LegacyDec,
	pubKeyByAddr map[string][]byte,
) ([]validatorEntry, error) {
	bootstrapShares := types.EqualBootstrapShares(len(committee))
	inCommittee := make(map[string]bool, len(committee))
	entries := make([]validatorEntry, 0, len(committee)+len(rampedByAddr))

	for i, m := range committee {
		// x/power's internal collections (and rampedByAddr/pubKeyByAddr, built from x/staking's
		// own OperatorAddress) are all keyed by the validator-operator ("valoper") bech32 form; a
		// BootstrapMember.OperatorAddress is stored in genesis as the plain account form (see
		// keeper.valoperFromAccountBech32's doc comment). This lookup was already validated at
		// InitGenesis, so an error here would indicate corrupted state, not bad input.
		valAddrStr, err := valoperFromAccountBech32(m.OperatorAddress)
		if err != nil {
			return nil, fmt.Errorf("corrupted bootstrap committee operator_address %q: %w", m.OperatorAddress, err)
		}
		inCommittee[valAddrStr] = true

		bootstrap := math.LegacyZeroDec()
		if eligible[valAddrStr] {
			bootstrap = bootstrapShares[i]
		}

		// Security review H2: never fall back to the genesis pubkey once a real staking record
		// exists for this operator, even if that record is no longer eligible (jailed/unbonded) -
		// its bootstrap contribution is already zeroed above; the pubkey used for any REMAINING
		// power (a real self-bond's ramped capped share) must always be the current staking
		// record's own key, not a stale genesis one.
		pk := pubKeyByAddr[valAddrStr]
		if pk == nil {
			pk = m.ConsensusPubkey
		}
		ramped, ok := rampedByAddr[valAddrStr]
		if !ok {
			ramped = math.LegacyZeroDec()
		}
		unramped, ok := unrampedByAddr[valAddrStr]
		if !ok {
			unramped = math.LegacyZeroDec()
		}
		entries = append(entries, validatorEntry{
			operatorAddr:   valAddrStr,
			pubKeyBytes:    pk,
			bootstrap:      bootstrap,
			rampedCapped:   ramped,
			unrampedCapped: unramped,
		})
	}

	// Outsiders: every bonded validator not already counted as a committee member. rampedByAddr
	// was built in computePowers by iterating x/staking's own deterministic bonded-validators
	// order, so ranging over it here (a Go map) would NOT be deterministic on its own - instead we
	// collect the keys and sort them before use.
	outsiders := make([]string, 0, len(rampedByAddr))
	for addr := range rampedByAddr {
		if inCommittee[addr] {
			continue
		}
		outsiders = append(outsiders, addr)
	}
	types.SortAddresses(outsiders)
	for _, addr := range outsiders {
		entries = append(entries, validatorEntry{
			operatorAddr:   addr,
			pubKeyBytes:    pubKeyByAddr[addr],
			bootstrap:      math.LegacyZeroDec(),
			rampedCapped:   rampedByAddr[addr],
			unrampedCapped: unrampedByAddr[addr],
		})
	}

	return entries, nil
}

// lastPower returns the last CometBFT power recorded for addr, and whether one was recorded.
func (k Keeper) lastPower(ctx sdk.Context, addr string) (int64, bool, error) {
	power, err := k.LastPower.Get(ctx, addr)
	if err == nil {
		return power, true, nil
	}
	if isNotFound(err) {
		return 0, false, nil
	}
	return 0, false, fmt.Errorf("failed to load last power for %q: %w", addr, err)
}

// recordAndBuildUpdate records addr's new power/pubkey and returns the ValidatorUpdate for it.
func (k Keeper) recordAndBuildUpdate(ctx sdk.Context, addr string, pubKeyBytes []byte, power int64) (abci.ValidatorUpdate, error) {
	if power > 0 {
		if err := k.LastPower.Set(ctx, addr, power); err != nil {
			return abci.ValidatorUpdate{}, fmt.Errorf("failed to record power for %q: %w", addr, err)
		}
		if err := k.LastPubKey.Set(ctx, addr, pubKeyBytes); err != nil {
			return abci.ValidatorUpdate{}, fmt.Errorf("failed to record pubkey for %q: %w", addr, err)
		}
	} else {
		if err := k.LastPower.Remove(ctx, addr); err != nil {
			return abci.ValidatorUpdate{}, fmt.Errorf("failed to clear power for %q: %w", addr, err)
		}
		// Security review, non-blocking "prune stale ramp and pubkey entries": once a validator's
		// power drops to zero and a removal update has been sent, its pubkey is no longer needed
		// (a future re-activation will supply a fresh one from live state).
		if err := k.LastPubKey.Remove(ctx, addr); err != nil {
			return abci.ValidatorUpdate{}, fmt.Errorf("failed to clear pubkey for %q: %w", addr, err)
		}
	}

	pkProto, err := pubKeyProto(pubKeyBytes)
	if err != nil {
		return abci.ValidatorUpdate{}, fmt.Errorf("failed to encode pubkey for %q: %w", addr, err)
	}
	return abci.ValidatorUpdate{PubKey: pkProto, Power: power}, nil
}

// removeDroppedValidators emits a zero-power update for every operator address that had a
// recorded LastPower but is not in seen (this block's full universe) - e.g. an outsider that fully
// unbonded, or a committee member whose bootstrap share lapsed at lambda == 1 (or was zeroed by
// jailing/tombstoning) with no stake of their own.
func (k Keeper) removeDroppedValidators(ctx sdk.Context, seen map[string]bool) ([]abci.ValidatorUpdate, error) {
	var toRemove []string
	if err := k.LastPower.Walk(ctx, nil, func(addr string, _ int64) (bool, error) {
		if !seen[addr] {
			toRemove = append(toRemove, addr)
		}
		return false, nil
	}); err != nil {
		return nil, fmt.Errorf("failed to walk last power records: %w", err)
	}

	updates := make([]abci.ValidatorUpdate, 0, len(toRemove))
	for _, addr := range toRemove {
		pkBytes, err := k.LastPubKey.Get(ctx, addr)
		if err != nil {
			return nil, fmt.Errorf("failed to load last pubkey for %q: %w", addr, err)
		}
		if err := k.LastPower.Remove(ctx, addr); err != nil {
			return nil, fmt.Errorf("failed to clear power for %q: %w", addr, err)
		}
		if err := k.LastPubKey.Remove(ctx, addr); err != nil {
			return nil, fmt.Errorf("failed to clear pubkey for %q: %w", addr, err)
		}
		pkProto, err := pubKeyProto(pkBytes)
		if err != nil {
			return nil, fmt.Errorf("failed to encode pubkey for %q: %w", addr, err)
		}
		updates = append(updates, abci.ValidatorUpdate{PubKey: pkProto, Power: 0})
	}
	return updates, nil
}

// consPubKeyBytes returns v's consensus pubkey as raw ed25519 bytes, or an error if it is any
// other key type. x/power only supports ed25519 consensus keys (security review L4: this chain's
// consensus params - see app/config.go - permit only PubKeyTypeEd25519, so a non-ed25519 consensus
// key can never actually be registered by a real validator; this check is a second, explicit line
// of defense against ever silently misinterpreting a different key type's bytes).
func consPubKeyBytes(v stakingtypes.Validator) ([]byte, error) {
	pk, err := v.ConsPubKey()
	if err != nil {
		return nil, err
	}
	ed, ok := pk.(*ed25519.PubKey)
	if !ok {
		return nil, fmt.Errorf("validator %q has a %T consensus key, only ed25519 is supported by x/power", v.OperatorAddress, pk)
	}
	return ed.Key, nil
}

// pubKeyProto wraps raw ed25519 pubkey bytes into the protobuf form abci.ValidatorUpdate expects.
// Every consensus key on this chain is ed25519 (x/power's committee members are ed25519-only by
// construction - see types.BootstrapMember - and a real staking validator created via
// MsgCreateValidator on this chain likewise always uses one, the CometBFT and cosmos-sdk default,
// enforced by this chain's consensus params - see consPubKeyBytes).
func pubKeyProto(raw []byte) (cmtprotocrypto.PublicKey, error) {
	pk := &ed25519.PubKey{Key: append([]byte(nil), raw...)}
	return cryptocodec.ToCmtProtoPublicKey(pk)
}

var _ cryptotypes.PubKey = (*ed25519.PubKey)(nil)
