package types_test

import (
	"fmt"
	"testing"

	"cosmossdk.io/math"

	"github.com/DeBrosOfficial/network/chain/x/power/types"
)

func dec(s string) math.LegacyDec { return math.LegacyMustNewDecFromStr(s) }

// unboundedMultiplier is used by every test that predates security review H3(b) (bounded
// redistribution): a multiplier this large never actually binds, so ComputeCappedShares behaves
// exactly as it did before the bound was added. Tests that check the bound itself use a realistic
// value instead (see TestComputeCappedShares_dustValidatorsBoundedByMultiplier).
var unboundedMultiplier = math.LegacyNewDec(1_000_000)

func TestComputeLambda_zeroStakeReachesOneAtDeadline(t *testing.T) {
	exitStake := math.NewInt(1_000_000)
	lambda := math.LegacyZeroDec()
	for epoch := uint64(1); epoch < 365; epoch++ {
		lambda = types.ComputeLambda(lambda, math.ZeroInt(), exitStake, epoch, 365)
		if lambda.Equal(math.LegacyOneDec()) {
			t.Fatalf("lambda reached 1 early at epoch %d with zero stake", epoch)
		}
	}
	lambda = types.ComputeLambda(lambda, math.ZeroInt(), exitStake, 365, 365)
	if !lambda.Equal(math.LegacyOneDec()) {
		t.Fatalf("lambda = %s at the deadline with zero stake, want 1", lambda)
	}
}

func TestComputeLambda_reachesOneEarlyAtStakeThreshold(t *testing.T) {
	exitStake := math.NewInt(1_000_000)
	lambda := types.ComputeLambda(math.LegacyZeroDec(), exitStake, exitStake, 10, 365)
	if !lambda.Equal(math.LegacyOneDec()) {
		t.Fatalf("lambda = %s at exactly bootstrap_exit_stake (epoch 10 of 365), want 1", lambda)
	}
}

func TestComputeLambda_monotonicUnderFluctuatingStake(t *testing.T) {
	exitStake := math.NewInt(1_000_000)
	lambda := math.LegacyZeroDec()
	stakes := []int64{100_000, 50_000, 0, 900_000, 200_000, 1_000_000, 0}
	for i, s := range stakes {
		next := types.ComputeLambda(lambda, math.NewInt(s), exitStake, uint64(i), 365)
		if next.LT(lambda) {
			t.Fatalf("lambda decreased from %s to %s at step %d (stake dropped to %d)", lambda, next, i, s)
		}
		lambda = next
	}
}

func TestComputeLambda_neverExceedsOne(t *testing.T) {
	lambda := types.ComputeLambda(math.LegacyZeroDec(), math.NewInt(10_000_000), math.NewInt(1), 100_000, 365)
	if !lambda.Equal(math.LegacyOneDec()) {
		t.Fatalf("lambda = %s, want capped at exactly 1", lambda)
	}
}

func TestComputeLambda_exactValues(t *testing.T) {
	// epochs_since_genesis=100, deadline=365 -> time term = 100/365; stake term = 200_000/1_000_000 = 0.2.
	// max(0, 0.2, 100/365=0.27397...) = 0.273972602739726027 (LegacyDec truncates the repeating decimal to
	// 18 digits without rounding).
	got := types.ComputeLambda(math.LegacyZeroDec(), math.NewInt(200_000), math.NewInt(1_000_000), 100, 365)
	want := dec("0.273972602739726027")
	if !got.Equal(want) {
		t.Fatalf("lambda = %s, want %s", got, want)
	}
}

func TestUpdateCapState_stepsDownAbove60(t *testing.T) {
	prev := types.CapState{CurrentCapBps: types.CapBpsNormal}
	p := types.DefaultParams()
	got := types.UpdateCapState(prev, 61, p)
	if got.CurrentCapBps != types.CapBpsReduced {
		t.Fatalf("cap = %d bps at 61 active validators, want reduced (%d)", got.CurrentCapBps, types.CapBpsReduced)
	}
	if got.BelowStepUpStreakEpochs != 0 {
		t.Fatalf("streak = %d, want 0", got.BelowStepUpStreakEpochs)
	}
}

func TestUpdateCapState_staysNormalAtOrBelow60(t *testing.T) {
	prev := types.CapState{CurrentCapBps: types.CapBpsNormal}
	p := types.DefaultParams()
	got := types.UpdateCapState(prev, 60, p)
	if got.CurrentCapBps != types.CapBpsNormal {
		t.Fatalf("cap = %d bps at 60 active validators, want normal (%d)", got.CurrentCapBps, types.CapBpsNormal)
	}
}

func TestUpdateCapState_hysteresisRequiresThirtyConsecutiveEpochsBelow50(t *testing.T) {
	p := types.DefaultParams() // CapStepUpValidatorCount=50, CapHysteresisEpochs=30
	state := types.CapState{CurrentCapBps: types.CapBpsReduced}

	for epoch := 1; epoch <= 29; epoch++ {
		state = types.UpdateCapState(state, 49, p)
		if state.CurrentCapBps != types.CapBpsReduced {
			t.Fatalf("epoch %d: cap stepped up early (bps=%d)", epoch, state.CurrentCapBps)
		}
	}
	if state.BelowStepUpStreakEpochs != 29 {
		t.Fatalf("streak = %d after 29 epochs below 50, want 29", state.BelowStepUpStreakEpochs)
	}

	state = types.UpdateCapState(state, 49, p)
	if state.CurrentCapBps != types.CapBpsNormal {
		t.Fatalf("cap did not step up on the 30th consecutive epoch below 50 (bps=%d)", state.CurrentCapBps)
	}
	if state.BelowStepUpStreakEpochs != 0 {
		t.Fatalf("streak = %d after stepping up, want reset to 0", state.BelowStepUpStreakEpochs)
	}
}

func TestUpdateCapState_streakResetsWhenCountRisesBackTo50OrAbove(t *testing.T) {
	p := types.DefaultParams()
	state := types.CapState{CurrentCapBps: types.CapBpsReduced}
	for i := 0; i < 20; i++ {
		state = types.UpdateCapState(state, 49, p)
	}
	if state.BelowStepUpStreakEpochs != 20 {
		t.Fatalf("streak = %d, want 20", state.BelowStepUpStreakEpochs)
	}
	state = types.UpdateCapState(state, 55, p) // back above the step-up threshold
	if state.BelowStepUpStreakEpochs != 0 || state.CurrentCapBps != types.CapBpsReduced {
		t.Fatalf("state = %+v, want streak reset to 0 and cap still reduced", state)
	}
}

func TestComputeCappedShares_bindsAndRedistributes(t *testing.T) {
	stakes := []types.ValidatorStake{
		{OperatorAddress: "a", BondedTokens: math.NewInt(70)},
		{OperatorAddress: "b", BondedTokens: math.NewInt(20)},
		{OperatorAddress: "c", BondedTokens: math.NewInt(10)},
	}
	shares := types.ComputeCappedShares(stakes, dec("0.5"), unboundedMultiplier)

	want := []math.LegacyDec{dec("0.5"), dec("0.333333333333333333"), dec("0.166666666666666667")}
	for i := range shares {
		if !shares[i].Equal(want[i]) {
			t.Fatalf("share[%d] = %s, want %s", i, shares[i], want[i])
		}
	}
	sum := shares[0].Add(shares[1]).Add(shares[2])
	if !sum.Equal(math.LegacyOneDec()) {
		t.Fatalf("shares sum to %s, want exactly 1", sum)
	}
}

func TestComputeCappedShares_noCappingWhenAllBelowCap(t *testing.T) {
	stakes := []types.ValidatorStake{
		{OperatorAddress: "a", BondedTokens: math.NewInt(1)},
		{OperatorAddress: "b", BondedTokens: math.NewInt(1)},
		{OperatorAddress: "c", BondedTokens: math.NewInt(1)},
	}
	shares := types.ComputeCappedShares(stakes, dec("0.5"), unboundedMultiplier)
	third := dec("0.333333333333333333")
	if !shares[0].Equal(third) || !shares[1].Equal(third) {
		t.Fatalf("shares = %v, want each ~1/3", shares)
	}
}

func TestComputeCappedShares_equalFallbackWhenCapInfeasible(t *testing.T) {
	// cap*n = 0.05*3 = 0.15 < 1: even an equal split would exceed the cap, so it can't bind at all.
	stakes := []types.ValidatorStake{
		{OperatorAddress: "a", BondedTokens: math.NewInt(90)},
		{OperatorAddress: "b", BondedTokens: math.NewInt(9)},
		{OperatorAddress: "c", BondedTokens: math.NewInt(1)},
	}
	shares := types.ComputeCappedShares(stakes, dec("0.05"), unboundedMultiplier)
	third := dec("0.333333333333333333")
	if !shares[0].Equal(third) || !shares[1].Equal(third) {
		t.Fatalf("shares = %v, want the equal fallback (~1/3 each)", shares)
	}
}

func TestComputeCappedShares_equalFallbackWhenAllStakesZero(t *testing.T) {
	stakes := []types.ValidatorStake{
		{OperatorAddress: "a", BondedTokens: math.ZeroInt()},
		{OperatorAddress: "b", BondedTokens: math.ZeroInt()},
	}
	shares := types.ComputeCappedShares(stakes, dec("0.5"), unboundedMultiplier)
	if !shares[0].Equal(dec("0.5")) || !shares[1].Equal(dec("0.5")) {
		t.Fatalf("shares = %v, want 0.5/0.5 equal fallback", shares)
	}
}

// TestComputeCappedShares_splittingBelowCapGainsNothing is the property test called for in
// track-c-chain.md C4: a validator that splits its stake into two identically-controlled
// validators, each still below the cap, ends up with exactly the same combined share as if it had
// stayed as one validator - the algorithm has no incentive to Sybil below the cap.
func TestComputeCappedShares_splittingBelowCapGainsNothing(t *testing.T) {
	cap := dec("0.5")
	others := []types.ValidatorStake{
		{OperatorAddress: "other1", BondedTokens: math.NewInt(300)},
		{OperatorAddress: "other2", BondedTokens: math.NewInt(400)},
	}

	unsplit := append([]types.ValidatorStake{{OperatorAddress: "whole", BondedTokens: math.NewInt(300)}}, others...)
	unsplitShares := types.ComputeCappedShares(unsplit, cap, unboundedMultiplier)

	split := append([]types.ValidatorStake{
		{OperatorAddress: "half-a", BondedTokens: math.NewInt(150)},
		{OperatorAddress: "half-b", BondedTokens: math.NewInt(150)},
	}, others...)
	splitShares := types.ComputeCappedShares(split, cap, unboundedMultiplier)

	combinedSplitShare := splitShares[0].Add(splitShares[1])
	if !combinedSplitShare.Equal(unsplitShares[0]) {
		t.Fatalf("splitting gained share: whole=%s split-combined=%s", unsplitShares[0], combinedSplitShare)
	}
	// And the two halves split the combined share exactly evenly with each other.
	if !splitShares[0].Equal(splitShares[1]) {
		t.Fatalf("the two halves got unequal shares: %s vs %s", splitShares[0], splitShares[1])
	}
}

func TestComputeCappedShares_splittingAboveCapGainsNothingEither(t *testing.T) {
	// A single whale at 90% of stake, cap=0.3: splitting into 3 equal validators of 30 each still
	// only nets 3*cap=0.9 combined if that's feasible, or less; it never exceeds what redistribution
	// would have given a single entity capped at 0.3 plus whatever a single capped entity could not
	// have captured anyway. Here we just check no single split part exceeds the cap and the combined
	// total does not exceed 3*cap.
	stakes := []types.ValidatorStake{
		{OperatorAddress: "w1", BondedTokens: math.NewInt(30)},
		{OperatorAddress: "w2", BondedTokens: math.NewInt(30)},
		{OperatorAddress: "w3", BondedTokens: math.NewInt(30)},
		{OperatorAddress: "minnow", BondedTokens: math.NewInt(10)},
	}
	shares := types.ComputeCappedShares(stakes, dec("0.3"), unboundedMultiplier)
	for i, s := range shares[:3] {
		if s.GT(dec("0.3")) {
			t.Fatalf("share[%d] = %s exceeds the cap", i, s)
		}
	}
}

func TestRampFactor(t *testing.T) {
	cases := []struct {
		name            string
		activation, now uint64
		rampEpochs      uint64
		want            math.LegacyDec
	}{
		{"at activation", 100, 100, 30, math.LegacyZeroDec()},
		{"before activation", 100, 50, 30, math.LegacyZeroDec()},
		{"halfway", 100, 115, 30, dec("0.5")},
		{"at full ramp", 100, 130, 30, math.LegacyOneDec()},
		{"past full ramp", 100, 500, 30, math.LegacyOneDec()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := types.RampFactor(tc.activation, tc.now, tc.rampEpochs)
			if !got.Equal(tc.want) {
				t.Fatalf("RampFactor(%d,%d,%d) = %s, want %s", tc.activation, tc.now, tc.rampEpochs, got, tc.want)
			}
		})
	}
}

func TestComputePower(t *testing.T) {
	// lambda=0.5, bootstrap share 0.1, ramped capped share 0.02: P = 0.5*0.1 + 0.5*0.02 = 0.06.
	got := types.ComputePower(dec("0.1"), dec("0.02"), dec("0.5"))
	want := dec("0.06")
	if !got.Equal(want) {
		t.Fatalf("ComputePower = %s, want %s", got, want)
	}
}

func TestComputePower_outsiderFollowsLambda(t *testing.T) {
	// An outsider (bootstrap=0) with a capped share of 0.1: at lambda=0 they have zero power, and
	// their power rises linearly with lambda up to the full capped share at lambda=1.
	if p := types.ComputePower(math.LegacyZeroDec(), dec("0.1"), math.LegacyZeroDec()); !p.IsZero() {
		t.Fatalf("outsider power at lambda=0 = %s, want 0", p)
	}
	if p := types.ComputePower(math.LegacyZeroDec(), dec("0.1"), dec("0.5")); !p.Equal(dec("0.05")) {
		t.Fatalf("outsider power at lambda=0.5 = %s, want 0.05", p)
	}
	if p := types.ComputePower(math.LegacyZeroDec(), dec("0.1"), math.LegacyOneDec()); !p.Equal(dec("0.1")) {
		t.Fatalf("outsider power at lambda=1 = %s, want 0.1", p)
	}
}

func TestComputePower_committeeSeatLapsesAtLambdaOne(t *testing.T) {
	// A committee-only member (bootstrap=0.1, no stake so capped=0) has power (1-lambda)*0.1, which
	// is exactly 0 once lambda reaches 1 - "committee seats lapse at lambda=1".
	p := types.ComputePower(dec("0.1"), math.LegacyZeroDec(), math.LegacyOneDec())
	if !p.IsZero() {
		t.Fatalf("committee-only power at lambda=1 = %s, want exactly 0", p)
	}
}

func TestPowerToCometBFT(t *testing.T) {
	if got := types.PowerToCometBFT(math.LegacyZeroDec(), 1_000_000_000); got != 0 {
		t.Fatalf("PowerToCometBFT(0) = %d, want 0", got)
	}
	if got := types.PowerToCometBFT(dec("0.05"), 1_000_000_000); got != 50_000_000 {
		t.Fatalf("PowerToCometBFT(0.05) = %d, want 50,000,000", got)
	}
	// A tiny but strictly positive share must never floor to 0 (that would be a removal update).
	if got := types.PowerToCometBFT(dec("0.0000000001"), 1_000_000_000); got != 1 {
		t.Fatalf("PowerToCometBFT(tiny positive) = %d, want floored up to 1", got)
	}
}

func TestEqualBootstrapShares_sumsToExactlyOne(t *testing.T) {
	for _, n := range []int{1, 2, 3, 7, 30, 100} {
		shares := types.EqualBootstrapShares(n)
		if len(shares) != n {
			t.Fatalf("EqualBootstrapShares(%d) returned %d shares", n, len(shares))
		}
		sum := math.LegacyZeroDec()
		for _, s := range shares {
			sum = sum.Add(s)
		}
		if !sum.Equal(math.LegacyOneDec()) {
			t.Fatalf("EqualBootstrapShares(%d) sums to %s, want exactly 1", n, sum)
		}
	}
}

// TestComputeCappedShares_dustValidatorsBoundedByMultiplier reproduces the security review H3
// dust-attack illustration: 10 honest validators sitting at the 5% cap, plus 10 "dust" validators
// each holding a single unit of stake. Without a redistribution bound, water-filling would
// redistribute the honest validators' excess to the dust validators until THEY ALSO hit the 5% cap
// (10 * 5% = 50% total for the attackers, from negligible real stake). With
// maxRedistributionMultiplier bounding each dust validator's ceiling to a small multiple of its own
// raw proportion, its actual share stays far below the cap.
func TestComputeCappedShares_dustValidatorsBoundedByMultiplier(t *testing.T) {
	stakes := make([]types.ValidatorStake, 0, 20)
	for i := 0; i < 10; i++ {
		stakes = append(stakes, types.ValidatorStake{OperatorAddress: fmt.Sprintf("honest%d", i), BondedTokens: math.NewInt(1_000_000)})
	}
	for i := 0; i < 10; i++ {
		stakes = append(stakes, types.ValidatorStake{OperatorAddress: fmt.Sprintf("dust%d", i), BondedTokens: math.NewInt(1)})
	}
	cap := dec("0.05")
	multiplier := math.LegacyNewDec(2)

	unbounded := types.ComputeCappedShares(stakes, cap, unboundedMultiplier)
	// Without a bound, every dust validator would also hit the 5% cap (their combined 10 units of
	// stake is negligible against the honest validators' 10,000,000, so nothing stops water-filling
	// redistributing the honest excess all the way up to their cap too).
	for i := 10; i < 20; i++ {
		if !unbounded[i].Equal(cap) {
			t.Fatalf("unbounded dust[%d] = %s, want exactly the cap %s (sanity-checking the attack exists)", i-10, unbounded[i], cap)
		}
	}

	bounded := types.ComputeCappedShares(stakes, cap, multiplier)
	rawDustProportion := dec("1").Quo(dec("10000010")) // 1 / (10*1,000,000 + 10*1)
	dustCeiling := rawDustProportion.Mul(multiplier)
	for i := 10; i < 20; i++ {
		if bounded[i].GT(dustCeiling) {
			t.Fatalf("bounded dust[%d] = %s, want at most %s (2x its own raw proportion)", i-10, bounded[i], dustCeiling)
		}
	}
	// Every honest validator is still capped at exactly the normal cap.
	for i := 0; i < 10; i++ {
		if !bounded[i].Equal(cap) {
			t.Fatalf("bounded honest[%d] = %s, want exactly the cap %s", i, bounded[i], cap)
		}
	}
	// The combined dust share is now a rounding error, not 50%.
	dustTotal := types.SumShares(bounded[10:])
	if dustTotal.GT(dec("0.01")) {
		t.Fatalf("combined dust share = %s, want well under 1%%", dustTotal)
	}
}

func TestHandoverGateThreshold(t *testing.T) {
	cases := []struct {
		capFraction string
		want        uint64
	}{
		{"0.05", 40}, // 2*ceil(1/0.05) = 2*20 = 40
		{"0.03", 68}, // 2*ceil(1/0.03) = 2*ceil(33.33) = 2*34 = 68
		{"0.5", 4},   // 2*ceil(1/0.5) = 2*2 = 4
	}
	for _, tc := range cases {
		got := types.HandoverGateThreshold(dec(tc.capFraction))
		if got != tc.want {
			t.Fatalf("HandoverGateThreshold(%s) = %d, want %d", tc.capFraction, got, tc.want)
		}
	}
}

func TestSumShares(t *testing.T) {
	sum := types.SumShares([]math.LegacyDec{dec("0.3"), dec("0.2"), dec("0.05")})
	if !sum.Equal(dec("0.55")) {
		t.Fatalf("SumShares = %s, want 0.55", sum)
	}
	if !types.SumShares(nil).IsZero() {
		t.Fatalf("SumShares(nil) should be zero")
	}
}

func TestBpsFromFraction(t *testing.T) {
	if got := types.BpsFromFraction(dec("0.05")); got != 500 {
		t.Fatalf("BpsFromFraction(0.05) = %d, want 500", got)
	}
	if got := types.BpsFromFraction(dec("0.03")); got != 300 {
		t.Fatalf("BpsFromFraction(0.03) = %d, want 300", got)
	}
}

func TestUpdateCapState_usesParamsFractionsNotHardcodedConstants(t *testing.T) {
	// A chain configured with NON-default cap fractions (7%/4%, instead of the 5%/3% defaults)
	// must step between 700/400 bps, not the hardcoded 500/300 (security review, non-blocking
	// "dead params").
	p := types.DefaultParams()
	p.CapFractionNormal = dec("0.07")
	p.CapFractionReduced = dec("0.04")

	got := types.UpdateCapState(types.CapState{CurrentCapBps: 700}, p.CapStepDownValidatorCount+1, p)
	if got.CurrentCapBps != 400 {
		t.Fatalf("cap bps = %d, want 400 (cap_fraction_reduced=4%%) for a non-default Params", got.CurrentCapBps)
	}
}
