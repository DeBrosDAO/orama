// Package types implements x/power's pure, deterministic power-computation state machine
// (plans/open-network/track-c-chain.md C4): the hand-over factor lambda, the capped stake share
// with ICS-style redistribution, the per-validator ramp, and the fixed-point mapping from a power
// share to a CometBFT integer voting power. Every function in this file is a pure function of its
// arguments (no time.Now, no map iteration, no floating point), so the same inputs always produce
// the same outputs on every node.
package types

import (
	"sort"

	"cosmossdk.io/math"
)

// ValidatorStake is one validator's bonded stake, used as input to ComputeCappedShares.
type ValidatorStake struct {
	// OperatorAddress is the validator's bech32 operator (valoper) address.
	OperatorAddress string
	// BondedTokens is the validator's total bonded stake (norama), including delegations.
	BondedTokens math.Int
}

// ComputeLambda returns the recomputed hand-over factor lambda
// (plans/open-network/track-c-chain.md C4):
//
//	lambda = min(1, max(lambda_prev, bonded/bootstrap_exit_stake, epochs_since_genesis/bootstrap_deadline_epochs))
//
// lambda is monotonic by construction: the result is always >= prevLambda, since prevLambda is one
// of the three terms maxed together, and no argument can make it decrease. It reaches 1 no later
// than epochsSinceGenesis == deadlineEpochs regardless of bondedStake (the zero-stake case), and
// earlier if bondedStake reaches bootstrapExitStake first.
//
// If bootstrapExitStake is zero, the stake term is treated as already at 1 (a zero exit-stake
// threshold is trivially met by any non-negative bonded stake). If deadlineEpochs is zero, the
// time term is treated as already at 1 for the same reason. Neither is a valid genesis
// configuration (Params.Validate rejects a zero BootstrapDeadlineEpochs), but ComputeLambda stays
// total either way rather than dividing by zero.
func ComputeLambda(prevLambda math.LegacyDec, bondedStake, bootstrapExitStake math.Int, epochsSinceGenesis, deadlineEpochs uint64) math.LegacyDec {
	stakeTerm := math.LegacyOneDec()
	if bootstrapExitStake.IsPositive() {
		stakeTerm = math.LegacyNewDecFromInt(bondedStake).Quo(math.LegacyNewDecFromInt(bootstrapExitStake))
	}

	timeTerm := math.LegacyOneDec()
	if deadlineEpochs > 0 {
		timeTerm = math.LegacyNewDec(int64(epochsSinceGenesis)).Quo(math.LegacyNewDec(int64(deadlineEpochs)))
	}

	lambda := prevLambda
	if stakeTerm.GT(lambda) {
		lambda = stakeTerm
	}
	if timeTerm.GT(lambda) {
		lambda = timeTerm
	}
	if lambda.GT(math.LegacyOneDec()) {
		lambda = math.LegacyOneDec()
	}
	return lambda
}

// CapBpsNormal and CapBpsReduced are x/power's DEFAULT-params cap values in basis points (1% = 100
// bps): 5%/3%, matching DefaultParams' CapFractionNormal/CapFractionReduced. They are provided for
// convenience (genesis defaults, tests) only - UpdateCapState and GenesisState.Validate always
// derive the actual basis-point values from the Params in effect (BpsFromFraction), never from
// these constants, so a chain configured with different cap fractions still behaves correctly
// (security review, non-blocking "dead params").
const (
	CapBpsNormal   uint64 = 500
	CapBpsReduced  uint64 = 300
	bpsDenominator uint64 = 10_000
)

// CapFractionBps converts a cap in basis points to the Dec fraction ComputeCappedShares expects.
func CapFractionBps(bps uint64) math.LegacyDec {
	return math.LegacyNewDec(int64(bps)).QuoInt64(int64(bpsDenominator))
}

// BpsFromFraction converts a Dec fraction to basis points (the inverse of CapFractionBps),
// truncating any precision finer than a single basis point.
func BpsFromFraction(d math.LegacyDec) uint64 {
	return d.MulInt64(int64(bpsDenominator)).TruncateInt().Uint64()
}

// CapState is x/power's mutable per-block hysteresis bookkeeping for the active-validator cap
// (plans/open-network/track-c-chain.md C4's "hysteresis: it returns to 5% only if the count stays
// below 50 for 30 days").
type CapState struct {
	// CurrentCapBps is the cap presently in force, in basis points: BpsFromFraction of whichever of
	// Params.CapFractionNormal/CapFractionReduced is active.
	CurrentCapBps uint64
	// BelowStepUpStreakEpochs counts consecutive epochs the active validator count has stayed
	// below Params.CapStepUpValidatorCount while the reduced cap is in force. It resets to 0
	// whenever the count is not below that threshold, or once the cap steps back up.
	BelowStepUpStreakEpochs uint64
}

// UpdateCapState recomputes CapState for the epoch that just closed, given the number of
// currently active (bonded) validators. It never changes more than one epoch's worth of
// hysteresis at a time, so it must be called exactly once per closed epoch, in epoch order. The
// two basis-point values it can return are always derived from the GIVEN Params
// (BpsFromFraction(p.CapFractionNormal/CapFractionReduced)), never a hardcoded constant, so a chain
// configured with non-default cap fractions steps between the RIGHT two values.
func UpdateCapState(prev CapState, activeValidatorCount uint64, p Params) CapState {
	normalBps := BpsFromFraction(p.CapFractionNormal)
	reducedBps := BpsFromFraction(p.CapFractionReduced)

	if activeValidatorCount > p.CapStepDownValidatorCount {
		// Above the step-down threshold: always reduced, streak irrelevant.
		return CapState{CurrentCapBps: reducedBps, BelowStepUpStreakEpochs: 0}
	}

	if prev.CurrentCapBps != reducedBps {
		// Already at (or starting at) the normal cap, and not above the step-down threshold: stay
		// normal.
		return CapState{CurrentCapBps: normalBps, BelowStepUpStreakEpochs: 0}
	}

	// Currently reduced, and the count is at or below the step-down threshold: hysteresis governs
	// whether it steps back up.
	if activeValidatorCount >= p.CapStepUpValidatorCount {
		return CapState{CurrentCapBps: reducedBps, BelowStepUpStreakEpochs: 0}
	}
	streak := prev.BelowStepUpStreakEpochs + 1
	if streak >= p.CapHysteresisEpochs {
		return CapState{CurrentCapBps: normalBps, BelowStepUpStreakEpochs: 0}
	}
	return CapState{CurrentCapBps: reducedBps, BelowStepUpStreakEpochs: streak}
}

// ComputeCappedShares implements the capped linear stake share C_i from
// plans/open-network/track-c-chain.md C4: each validator's share of total bonded stake, capped at
// cap, with the excess above cap redistributed proportionally among not-yet-capped validators
// (ICS power-shaping's "water-filling" algorithm), iterated until no further validator is newly
// capped. If cap*n < 1 (the cap can't be respected by any distribution, because even an exactly
// equal split would exceed it), every validator instead gets an equal 1/n share (the "equal
// fallback"). If every validator has zero bonded stake, the equal fallback also applies (there is
// no proportion to compute from).
//
// maxRedistributionMultiplier bounds each validator's EFFECTIVE ceiling during redistribution at
// min(cap, rawProportion_i * maxRedistributionMultiplier) (security review H3(b)): without this, a
// validator holding only a token stake can be redistributed all the way up to the full per-validator
// cap - the same absolute ceiling a large, honest validator gets - simply by existing as a separate
// identity, letting many such "dust" identities collectively capture a large share of voting power
// for negligible real stake (the attack the review illustrates: 10 honest validators at the cap
// plus 10 attackers holding one unit each can otherwise net the attackers 50%). Bounding each
// validator's redistribution gain to a multiple of its OWN raw proportion closes this: a dust
// validator's ceiling stays proportionally small no matter how much excess is available to
// redistribute.
//
// Because of this bound, the returned shares are NOT guaranteed to sum to 1 any more: if every
// still-uncapped validator hits its OWN (possibly tiny) ceiling before the cap's excess is fully
// redistributed, the leftover is simply left unallocated rather than forced onto someone past their
// bound. Callers must normalize by the actual sum of assigned shares (Keeper.normalizedShares) when
// they need a total-power-relative fraction, exactly as they already must during the ramp (where
// shares legitimately sum to less than 1 while validators are still ramping in).
//
// This is a pure function of its arguments: the caller is responsible for assembling stakes in a
// deterministic order (e.g. sorted by operator address), though the algorithm itself is
// order-independent - it only aggregates, it never breaks ties by position.
func ComputeCappedShares(stakes []ValidatorStake, cap, maxRedistributionMultiplier math.LegacyDec) []math.LegacyDec {
	n := len(stakes)
	if n == 0 {
		return nil
	}

	equal := equalShares(n)

	// Equal fallback: the cap can't be respected by any distribution at all.
	if cap.MulInt64(int64(n)).LT(math.LegacyOneDec()) {
		return equal
	}

	total := math.ZeroInt()
	for _, s := range stakes {
		total = total.Add(s.BondedTokens)
	}
	if !total.IsPositive() {
		return equal
	}
	totalDec := math.LegacyNewDecFromInt(total)

	shares := make([]math.LegacyDec, n)
	capped := make([]bool, n)
	rawProportion := make([]math.LegacyDec, n)
	ceiling := make([]math.LegacyDec, n)
	for i, s := range stakes {
		rawProportion[i] = math.LegacyNewDecFromInt(s.BondedTokens).Quo(totalDec)
		ceiling[i] = cap
		if maxRedistributionMultiplier.IsPositive() {
			if bound := rawProportion[i].Mul(maxRedistributionMultiplier); bound.LT(ceiling[i]) {
				ceiling[i] = bound
			}
		}
	}

	remaining := math.LegacyOneDec()
	remainingRawTotal := math.LegacyOneDec()

	// Each iteration caps at least one more validator (or leaves the set unchanged, at which
	// point it has converged), so this terminates in at most n iterations. Every validator still
	// uncapped in a given iteration is evaluated against the SAME snapshot of
	// remaining/remainingRawTotal (taken before the iteration mutates either), so the result does
	// not depend on the order stakes are visited in within that iteration - only on which
	// validators have been capped by previous iterations.
	for iter := 0; iter < n; iter++ {
		if remainingRawTotal.IsZero() {
			break
		}
		snapRemaining := remaining
		snapRawTotal := remainingRawTotal
		anyNewlyCapped := false
		for i := range stakes {
			if capped[i] {
				continue
			}
			tentative := snapRemaining.Mul(rawProportion[i]).Quo(snapRawTotal)
			if tentative.GT(ceiling[i]) {
				shares[i] = ceiling[i]
				capped[i] = true
				remaining = remaining.Sub(ceiling[i])
				remainingRawTotal = remainingRawTotal.Sub(rawProportion[i])
				anyNewlyCapped = true
			} else {
				shares[i] = tentative
			}
		}
		if !anyNewlyCapped {
			break
		}
	}

	return shares
}

// EqualBootstrapShares returns n equal Dec shares summing to exactly 1 - the bootstrap
// committee's B_i (plans/open-network/track-c-chain.md C4): 1/n_bootstrap for each of the n
// committee members. See equalShares for the exactness note.
func EqualBootstrapShares(n int) []math.LegacyDec {
	return equalShares(n)
}

// equalShares returns n equal Dec shares summing to exactly 1: the first n-1 entries get
// floor(1/n) at LegacyDec precision, and the last entry gets the remainder, so the sum is exact
// even though 1/n may not terminate in LegacyDec's 18-digit precision.
func equalShares(n int) []math.LegacyDec {
	shares := make([]math.LegacyDec, n)
	each := math.LegacyOneDec().QuoInt64(int64(n))
	sum := math.LegacyZeroDec()
	for i := 0; i < n-1; i++ {
		shares[i] = each
		sum = sum.Add(each)
	}
	shares[n-1] = math.LegacyOneDec().Sub(sum)
	return shares
}

// RampFactor returns the linear ramp multiplier for a validator's capped share
// (plans/open-network/track-c-chain.md C4's "30-day ramp"): 0 at activationEpoch, rising linearly
// to 1 at activationEpoch+rampEpochs and staying at 1 after. If currentEpoch is before
// activationEpoch (should not happen given how callers record activation, but guarded rather than
// underflowing), it returns 0.
func RampFactor(activationEpoch, currentEpoch, rampEpochs uint64) math.LegacyDec {
	if currentEpoch <= activationEpoch {
		return math.LegacyZeroDec()
	}
	if rampEpochs == 0 {
		return math.LegacyOneDec()
	}
	elapsed := currentEpoch - activationEpoch
	if elapsed >= rampEpochs {
		return math.LegacyOneDec()
	}
	return math.LegacyNewDec(int64(elapsed)).QuoInt64(int64(rampEpochs))
}

// ComputePower returns P_i = (1-lambda)*bootstrapShare + lambda*rampedCappedShare, the blended
// voting-power share from plans/open-network/track-c-chain.md C4. Summed across every validator in
// a block's universe, P_i can legitimately be less than 1 (during the ramp, while
// ComputeCappedShares' redistribution bound leaves mass unallocated, or while a jailed/tombstoned
// committee member's bootstrap share is zeroed - see Keeper.buildValidatorEntries) - callers that
// need a total-power-relative fraction must divide by the actual sum (Keeper.normalizedShares),
// never assume it is 1.
func ComputePower(bootstrapShare, rampedCappedShare, lambda math.LegacyDec) math.LegacyDec {
	return math.LegacyOneDec().Sub(lambda).Mul(bootstrapShare).Add(lambda.Mul(rampedCappedShare))
}

// HandoverGateThreshold returns the minimum number of real, stake-indexed bonded validators
// (security review H3(a)) that must be active at once before lambda may rise past
// Params.PreGateLambdaCap: 2*ceil(1/capFraction) - twice the number of validators it would take,
// each exactly at the cap, to reach 100% of stake-based power. Below this count, a small enough set
// of validators (or a single actor splitting stake across capFraction-sized identities) could
// otherwise take over consensus the moment lambda reaches 1, regardless of how little total stake
// backs them.
func HandoverGateThreshold(capFraction math.LegacyDec) uint64 {
	if !capFraction.IsPositive() {
		return 0
	}
	inv := math.LegacyOneDec().Quo(capFraction)
	threshold := inv.Ceil().TruncateInt64()
	return uint64(2 * threshold)
}

// SumShares adds a slice of Dec shares together (used to normalize power shares that may sum to
// less than 1 - see ComputePower's doc comment).
func SumShares(shares []math.LegacyDec) math.LegacyDec {
	sum := math.LegacyZeroDec()
	for _, s := range shares {
		sum = sum.Add(s)
	}
	return sum
}

// PowerToCometBFT maps a power share (a fraction of 1) to a CometBFT integer voting power, using
// the fixed-point scale documented on Params.CometPowerScale:
//
//	comet_power = floor(share * scale), floored up to 1 if share is strictly positive.
//
// A strictly positive share always maps to at least 1 so a validator with real (if tiny) power is
// never silently dropped from the validator set by rounding to 0, which CometBFT would otherwise
// treat as "remove this validator" (a zero-power ValidatorUpdate).
func PowerToCometBFT(share math.LegacyDec, scale int64) int64 {
	if !share.IsPositive() {
		return 0
	}
	power := share.MulInt64(scale).TruncateInt64()
	if power < 1 {
		return 1
	}
	return power
}

// SortValidatorStakes sorts stakes by OperatorAddress ascending, in place, and returns it. Callers
// that assemble a []ValidatorStake from an unordered source (e.g. a Go map, never used for chain
// state in this module, but a possibility for a test) must sort it this way before passing it to
// ComputeCappedShares so every node computes the same result from the same underlying set,
// independent of iteration order.
func SortValidatorStakes(stakes []ValidatorStake) []ValidatorStake {
	sort.Slice(stakes, func(i, j int) bool { return stakes[i].OperatorAddress < stakes[j].OperatorAddress })
	return stakes
}

// SortAddresses sorts a slice of bech32 addresses ascending, in place, and returns it. Used
// wherever a set of addresses is assembled from a Go map (an inherently unordered iteration) and
// must be put back into a deterministic order before it can affect any on-chain output.
func SortAddresses(addrs []string) []string {
	sort.Strings(addrs)
	return addrs
}
