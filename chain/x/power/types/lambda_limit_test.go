package types_test

import (
	"testing"

	"cosmossdk.io/math"

	"github.com/DeBrosOfficial/network/chain/x/power/types"
)

func TestClampLambdaStep_disjointHandoverMovesAtMostOneThird(t *testing.T) {
	// Committee holds all bootstrap power and no stake. One outsider holds all stake.
	// A jump from lambda 0 to 1 would hand the entire voting set over in one epoch.
	bootstrap := []math.LegacyDec{math.LegacyOneDec(), math.LegacyZeroDec()}
	ramped := []math.LegacyDec{math.LegacyZeroDec(), math.LegacyOneDec()}
	prev := math.LegacyZeroDec()
	candidate := math.LegacyOneDec()

	got := types.ClampLambdaStep(prev, candidate, bootstrap, ramped, types.MaxVotingPowerShiftPerEpoch())
	shift := types.VotingPowerShift(bootstrap, ramped, prev, got)
	if shift.GT(types.MaxVotingPowerShiftPerEpoch()) {
		t.Fatalf("clamped shift = %s, want at most 1/3", shift)
	}
	if !got.LT(candidate) {
		t.Fatalf("lambda = %s, want the full jump to 1 to be refused", got)
	}
	if !got.GT(prev) {
		t.Fatalf("lambda = %s, want the step to still move forward", got)
	}
}

func TestClampLambdaStepBoth_zeroPublishedRampStillBoundsLatentStake(t *testing.T) {
	// This block publishes no outsider power (the ramp is still zero), so a
	// lambda jump does not move the published distribution. The outsider
	// already holds the whole capped stake. That latent shift must still be
	// bounded, or the next ramp tick would hand over the validator set at once.
	bootstrap := []math.LegacyDec{math.LegacyOneDec(), math.LegacyZeroDec()}
	published := []math.LegacyDec{math.LegacyZeroDec(), math.LegacyZeroDec()}
	latent := []math.LegacyDec{math.LegacyZeroDec(), math.LegacyOneDec()}
	got := types.ClampLambdaStepBoth(math.LegacyZeroDec(), math.LegacyOneDec(), bootstrap, published, latent, types.MaxVotingPowerShiftPerEpoch())
	shift := types.VotingPowerShift(bootstrap, latent, math.LegacyZeroDec(), got)
	if shift.GT(types.MaxVotingPowerShiftPerEpoch()) {
		t.Fatalf("latent shift = %s, want at most 1/3", shift)
	}
	if !got.LT(math.LegacyOneDec()) {
		t.Fatalf("lambda = %s, want the full jump refused while latent stake would move more than 1/3", got)
	}
}

func TestClampLambdaStep_smallStepIsUnchanged(t *testing.T) {
	bootstrap := []math.LegacyDec{math.LegacyOneDec(), math.LegacyZeroDec()}
	ramped := []math.LegacyDec{math.LegacyZeroDec(), math.LegacyOneDec()}
	prev := dec("0.20")
	candidate := dec("0.30") // total variation equals 0.10, under 1/3
	got := types.ClampLambdaStep(prev, candidate, bootstrap, ramped, types.MaxVotingPowerShiftPerEpoch())
	if !got.Equal(candidate) {
		t.Fatalf("lambda = %s, want the candidate %s left unchanged", got, candidate)
	}
}

func TestClampLambdaStep_repeatedStepsReachOne(t *testing.T) {
	bootstrap := []math.LegacyDec{math.LegacyOneDec(), math.LegacyZeroDec()}
	ramped := []math.LegacyDec{math.LegacyZeroDec(), math.LegacyOneDec()}
	lambda := math.LegacyZeroDec()
	for step := 0; step < 6; step++ {
		lambda = types.ClampLambdaStep(lambda, math.LegacyOneDec(), bootstrap, ramped, types.MaxVotingPowerShiftPerEpoch())
		shift := types.VotingPowerShift(bootstrap, ramped, math.LegacyZeroDec(), lambda)
		if shift.GT(math.LegacyOneDec()) {
			t.Fatalf("shift %s exceeds 1", shift)
		}
	}
	if !lambda.Equal(math.LegacyOneDec()) {
		t.Fatalf("lambda after repeated steps = %s, want 1", lambda)
	}
}
