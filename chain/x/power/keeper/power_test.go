package keeper_test

import (
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/power/types"
)

func setupSingleCommitteeGenesis(t *testing.T, f *testFixture) sdk.AccAddress {
	t.Helper()
	f.Ctx = f.Ctx.WithChainID("orama-devnet-1")
	memberAddr := sdk.AccAddress("solo_committee_member")
	gs := types.DefaultGenesisState()
	gs.Params.MinCommitteeSize = 1
	gs.Params.BootstrapExitStake = math.NewInt(1_000_000)
	gs.Params.BootstrapDeadlineEpochs = 10
	gs.BootstrapCommittee = []types.BootstrapMember{{
		OperatorAddress: memberAddr.String(),
		Moniker:         "solo",
		ConsensusPubkey: testPubKey(1),
	}}
	// This fixture only ever has a handful of validators, far below the real dust-attack gate
	// threshold (security review H3(a): HandoverGateThreshold, ~40 validators at a 5% cap). Since
	// none of the tests using this helper exercise that gate itself (see power_test.go's dedicated
	// dust-attack tests for that), start with it already satisfied so lambda can reach 1 here.
	gs.GateSatisfied = true
	_, err := f.Keeper.InitGenesis(f.Ctx, *gs, f.Emission)
	require.NoError(t, err)
	return memberAddr
}

func TestRunEndBlock_committeeOnlyStaysAtEqualPowerBeforeLambdaMoves(t *testing.T) {
	f := newTestFixture(t)
	setupSingleCommitteeGenesis(t, f)

	f.Emission.epoch = 1 // same epoch as genesis_epoch: lambda must not advance yet.
	updates, err := f.Keeper.RunEndBlock(f.Ctx, f.Emission)
	require.NoError(t, err)
	require.Empty(t, updates, "power unchanged from genesis should produce no update")
}

func TestRunEndBlock_lambdaAdvancesAndOutsiderGainsPower(t *testing.T) {
	f := newTestFixture(t)
	setupSingleCommitteeGenesis(t, f)

	// Lambda is driven by max(stake ratio, time ratio) (types.ComputeLambda) - raise the exit-stake
	// threshold far above what this test's outsider ever bonds, so lambda advances purely on the
	// BootstrapDeadlineEpochs=10 time term below, not the moment the outsider's stake happens to
	// reach the default 1,000,000 exit-stake threshold.
	p, err := f.Keeper.Params.Get(f.Ctx)
	require.NoError(t, err)
	p.BootstrapExitStake = math.NewInt(1_000_000_000_000)
	// Params default to a 30-epoch ramp; disable it here so a brand-new outsider's power is
	// visible in the very block it appears, isolating this test from ramp behavior (covered
	// separately by TestRunEndBlock_rampsNewValidatorOverConfiguredEpochs).
	p.RampEpochs = 1
	require.NoError(t, f.Keeper.Params.Set(f.Ctx, p))

	outsiderValoper := sdk.ValAddress("outsider_validator_01").String()
	f.Staking.addValidator(t, outsiderValoper, testPubKey(2), 1_000_000, "0.0")

	// Prime the outsider's ramp activation well before the hand-over deadline, while lambda is
	// still below 1 (time term (5-1)/10 = 0.4) and the committee still holds positive bootstrap
	// power - if the outsider only appeared in the very same block lambda reaches 1, the committee's
	// bootstrap share would hit zero and the outsider's fresh (unramped) power would also still be
	// zero, momentarily emptying the whole validator set (the L5 guard RunEndBlock enforces against
	// - see its "would be empty" error).
	f.Emission.epoch = 5
	_, err = f.Keeper.RunEndBlock(f.Ctx, f.Emission)
	require.NoError(t, err)

	// Genesis epoch is 1 and BootstrapDeadlineEpochs is 10: lambda's time term reaches exactly 1 at
	// epoch 11 (epochsSinceGenesis 10 / deadlineEpochs 10). Advance one epoch at a time. A single
	// jump across several epochs would move more than a third of voting power, which the per-epoch
	// lambda rate limit refuses.
	var updates []abci.ValidatorUpdate
	for epoch := uint64(6); epoch <= 11; epoch++ {
		f.Emission.epoch = epoch
		updates, err = f.Keeper.RunEndBlock(f.Ctx, f.Emission)
		require.NoError(t, err)
	}

	lambda, err := f.Keeper.Lambda.Get(f.Ctx)
	require.NoError(t, err)
	require.True(t, lambda.Equal(math.LegacyOneDec()), "lambda = %s, want 1 at the deadline", lambda)

	// At lambda=1 the committee-only member (no stake) drops to 0 power (a removal update); the
	// outsider's ramp - stamped at epoch 5, well over its RampEpochs=1 before this block - is now
	// complete, so it picks up full power in this same block (an addition update).
	require.Len(t, updates, 2, "expected both the committee removal and the outsider's addition this block")

	memberValoper := sdk.ValAddress(sdk.AccAddress("solo_committee_member")).String()
	_, err = f.Keeper.LastPower.Get(f.Ctx, memberValoper)
	require.ErrorContains(t, err, "not found", "the lapsed committee seat should have been cleared from LastPower")

	// The outsider - the only staked validator, so its capped share is 1.0 - gets full power.
	outsiderPower, err := f.Keeper.LastPower.Get(f.Ctx, outsiderValoper)
	require.NoError(t, err)
	require.Equal(t, int64(1_000_000_000), outsiderPower)
}

func TestRunEndBlock_capBindsAcrossMultipleOutsiders(t *testing.T) {
	f := newTestFixture(t)
	setupSingleCommitteeGenesis(t, f)

	v1 := sdk.ValAddress("outsider_validator_v1").String()
	v2 := sdk.ValAddress("outsider_validator_v2").String()
	v3 := sdk.ValAddress("outsider_validator_v3").String()
	f.Staking.addValidator(t, v1, testPubKey(2), 700, "0.0")
	f.Staking.addValidator(t, v2, testPubKey(3), 200, "0.0")
	f.Staking.addValidator(t, v3, testPubKey(4), 100, "0.0")

	f.Emission.epoch = 10 // lambda -> 1
	_, err := f.Keeper.RunEndBlock(f.Ctx, f.Emission)
	require.NoError(t, err)

	// v1 is fresh this block, so its ramp factor is 0 - its ramped capped share is 0 regardless of
	// the cap. Confirm it is NOT simply capped at 5%; instead check the invariant that no validator
	// exceeds Params.CapFractionNormal once ramped in a later epoch.
	p, err := f.Keeper.Params.Get(f.Ctx)
	require.NoError(t, err)
	require.True(t, p.CapFractionNormal.Equal(math.LegacyNewDecWithPrec(5, 2)))
}

func TestRunEndBlock_rampsNewValidatorOverConfiguredEpochs(t *testing.T) {
	f := newTestFixture(t)
	setupSingleCommitteeGenesis(t, f)

	p, err := f.Keeper.Params.Get(f.Ctx)
	require.NoError(t, err)
	p.RampEpochs = 10
	// Lambda is driven by max(stake ratio, time ratio) (types.ComputeLambda) - raise the exit-stake
	// threshold far above what the two validators below ever bond, so lambda stays negligible while
	// they ramp up (letting the committee's bootstrap share cover the validator set in the
	// meantime), instead of jumping straight to 1 the moment a validator's stake happens to reach
	// the default 1,000,000 exit-stake threshold.
	p.BootstrapExitStake = math.NewInt(1_000_000_000_000)
	// A cap comfortably above 50% for two EQUAL-stake validators keeps ComputeCappedShares out of
	// its "equal fallback" branch (which only triggers when cap*validatorCount < 1 and would force
	// a 50/50 split regardless of actual stake), so each one's capped share is exactly its real
	// stake proportion (0.5). Normal and reduced are set equal so UpdateCapState's normal/reduced
	// hysteresis (irrelevant to what this test checks) can never change the value in use.
	// 90% stays above both the equal split and the halfway token-ramp split
	// (a fully ramped validator against one at half tokens is 2/3).
	p.CapFractionNormal = math.LegacyNewDecWithPrec(90, 2)
	p.CapFractionReduced = math.LegacyNewDecWithPrec(90, 2)
	require.NoError(t, f.Keeper.Params.Set(f.Ctx, p))

	// An anchor validator with the SAME stake as the one that will ramp below, given a head start to
	// fully complete its own ramp first (while lambda is still near 0, so the committee's bootstrap
	// share - not yet the anchor's own still-ramping ramp - covers the validator set). This gives
	// the ramping validator's later share of the ACTUAL total (never a nominal 1 - security review
	// B5/M5) a stable, non-zero baseline to be measured against - otherwise, being the only
	// validator with any power at all would trivially normalize its own share to 100% regardless of
	// how far along its ramp actually is.
	anchorValoper := sdk.ValAddress("anchor_validator_addr").String()
	f.Staking.addValidator(t, anchorValoper, testPubKey(3), 1_000_000, "0.0")
	f.Emission.epoch = 1
	_, err = f.Keeper.RunEndBlock(f.Ctx, f.Emission) // stamps the anchor's ramp activation
	require.NoError(t, err)
	f.Emission.epoch = 1 + 10 // anchor fully ramped (RampEpochs=10)
	_, err = f.Keeper.RunEndBlock(f.Ctx, f.Emission)
	require.NoError(t, err)

	// Pin lambda at 1 for the rest of this test: lambda's own transition is covered by
	// TestRunEndBlock_lambdaAdvancesAndOutsiderGainsPower, and this test only cares about the ramp
	// curve from here on - decoupled from that, and safe now that the anchor's own ramp is already
	// complete and will keep the validator set non-empty once the committee's bootstrap share drops
	// out (the L5 guard against ever emptying the validator set).
	require.NoError(t, f.Keeper.Lambda.Set(f.Ctx, math.LegacyOneDec()))
	require.NoError(t, f.Keeper.LambdaLastUpdatedEpoch.Set(f.Ctx, 1_000_000))

	outsiderValoper := sdk.ValAddress("ramping_validator_addr").String()
	f.Staking.addValidator(t, outsiderValoper, testPubKey(2), 1_000_000, "0.0")

	f.Emission.epoch = 11 // ramp activation stamped this epoch
	_, err = f.Keeper.RunEndBlock(f.Ctx, f.Emission)
	require.NoError(t, err)

	// Ramp activation was just stamped; power should still be 0 this same epoch.
	power, err := f.Keeper.LastPower.Get(f.Ctx, outsiderValoper)
	if err == nil {
		require.Zero(t, power)
	}

	// Halfway through the ramp the new validator's tokens count half and the anchor's count in full,
	// so the stake split is 1:2. With the cap above that split, normalized power is 1/3.
	f.Emission.epoch = 16
	_, err = f.Keeper.RunEndBlock(f.Ctx, f.Emission)
	require.NoError(t, err)
	power, err = f.Keeper.LastPower.Get(f.Ctx, outsiderValoper)
	require.NoError(t, err)
	require.InDelta(t, 333_333_333, power, 1_000_000, "expected roughly a third of full power halfway through the ramp")

	// Fully ramped (epoch 21 = activation(11) + rampEpochs(10)): both validators now have an equal
	// raw share (0.5 each), so the ramping one's normalized share is exactly half the real total.
	f.Emission.epoch = 21
	_, err = f.Keeper.RunEndBlock(f.Ctx, f.Emission)
	require.NoError(t, err)
	power, err = f.Keeper.LastPower.Get(f.Ctx, outsiderValoper)
	require.NoError(t, err)
	require.Equal(t, int64(500_000_000), power)
}

func TestRunEndBlock_laterBondKeepsRampProgress(t *testing.T) {
	f := newTestFixture(t)
	setupSingleCommitteeGenesis(t, f)
	p, err := f.Keeper.Params.Get(f.Ctx)
	require.NoError(t, err)
	p.RampEpochs = 10
	p.BootstrapExitStake = math.NewInt(1_000_000_000_000)
	p.CapFractionNormal = math.LegacyNewDecWithPrec(90, 2)
	p.CapFractionReduced = math.LegacyNewDecWithPrec(90, 2)
	require.NoError(t, f.Keeper.Params.Set(f.Ctx, p))

	anchor := sdk.ValAddress("later_bond_anchor___").String()
	f.Staking.addValidator(t, anchor, testPubKey(4), 1_000_000, "0.0")
	f.Emission.epoch = 1
	_, err = f.Keeper.RunEndBlock(f.Ctx, f.Emission)
	require.NoError(t, err)
	f.Emission.epoch = 11
	_, err = f.Keeper.RunEndBlock(f.Ctx, f.Emission)
	require.NoError(t, err)
	require.NoError(t, f.Keeper.Lambda.Set(f.Ctx, math.LegacyOneDec()))
	require.NoError(t, f.Keeper.LambdaLastUpdatedEpoch.Set(f.Ctx, 1_000_000_000))
	for i := 0; i < 6; i++ {
		_, err = f.Keeper.RunEndBlock(f.Ctx, f.Emission)
		require.NoError(t, err)
	}

	outsider := sdk.ValAddress("later_bond_outsider_").String()
	f.Staking.addValidator(t, outsider, testPubKey(5), 1_000_000, "0.0")
	f.Emission.epoch = 20
	_, err = f.Keeper.RunEndBlock(f.Ctx, f.Emission)
	require.NoError(t, err)
	f.Emission.epoch = 25
	_, err = f.Keeper.RunEndBlock(f.Ctx, f.Emission)
	require.NoError(t, err)
	before, err := f.Keeper.LastPower.Get(f.Ctx, outsider)
	require.NoError(t, err)
	require.Positive(t, before)

	f.Staking.validators[outsider].val.Tokens = math.NewInt(3_000_000)
	_, err = f.Keeper.RunEndBlock(f.Ctx, f.Emission)
	require.NoError(t, err)
	after, err := f.Keeper.LastPower.Get(f.Ctx, outsider)
	require.NoError(t, err)
	require.InDelta(t, float64(before), float64(after), 1_000_000, "a later bond must keep the stake already admitted by the ramp")
}
