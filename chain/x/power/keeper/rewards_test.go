package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
)

const testEmissionModule = "emission"

func TestDistributeEpochRewards_forceBondsFromTheFirstRewardEvenAtZeroInitialSelfBond(t *testing.T) {
	f := newTestFixture(t)
	memberAddr := setupSingleCommitteeGenesis(t, f)

	// InitGenesis already gave this committee member a real (Bonded, zero-token) validator record
	// (see types.StakingKeeper's doc comment) - it does not need to submit its own
	// MsgCreateValidator first. Delegate can bootstrap a zero-token/zero-share validator directly,
	// so its very first reward is already force-bond-eligible.
	f.Emission.epoch = 1
	f.Bank.fund(testEmissionModule, math.NewInt(1_000))

	distributed, err := f.Keeper.DistributeEpochRewards(f.Ctx, f.Emission, testEmissionModule, math.NewInt(1_000))
	require.NoError(t, err)
	require.Equal(t, math.NewInt(1_000), distributed)

	// Default params force-bond 50% of a committee member's own reward (its sole delegation, so
	// the whole 1,000 counts as "its own"), well under the default multi-million-norama ceiling.
	require.Equal(t, math.NewInt(500), f.Earnings.credited[memberAddr.String()])

	memberValoper := sdk.ValAddress(memberAddr).String()
	selfBondTotal, err := f.Keeper.CommitteeSelfBond.Get(f.Ctx, memberValoper)
	require.NoError(t, err)
	require.Equal(t, math.NewInt(500), selfBondTotal)
}

func TestDistributeEpochRewards_forceBondsHalfOfCommitteeMemberRewardOnceSelfBonded(t *testing.T) {
	f := newTestFixture(t)
	memberAddr := setupSingleCommitteeGenesis(t, f)
	memberValoper := sdk.ValAddress(memberAddr).String()

	// The member has since created a real validator with a small self-bond (as their first
	// earnings would let them do).
	f.Staking.addValidator(t, memberValoper, testPubKey(1), 100, "0.0")
	f.Bank.fund(memberAddr.String(), math.ZeroInt()) // account exists

	p, err := f.Keeper.Params.Get(f.Ctx)
	require.NoError(t, err)
	p.ForceBondFraction = math.LegacyNewDecWithPrec(50, 2) // 50%
	p.MinSelfBond = math.NewInt(1_000)
	p.SelfBondCapMultiplier = math.LegacyNewDec(2) // ceiling = 2,000
	require.NoError(t, f.Keeper.Params.Set(f.Ctx, p))

	f.Emission.epoch = 1
	f.Bank.fund(testEmissionModule, math.NewInt(1_000))

	distributed, err := f.Keeper.DistributeEpochRewards(f.Ctx, f.Emission, testEmissionModule, math.NewInt(1_000))
	require.NoError(t, err)
	require.Equal(t, math.NewInt(1_000), distributed)

	// 50% of the 1,000 reward (the member's own share: sole delegator, so it is all "commission")
	// is force-bonded; the rest is credited to earnings.
	require.Equal(t, math.NewInt(500), f.Earnings.credited[memberAddr.String()])

	selfBondTotal, err := f.Keeper.CommitteeSelfBond.Get(f.Ctx, memberValoper)
	require.NoError(t, err)
	require.Equal(t, math.NewInt(500), selfBondTotal)

	valAddr, err := sdk.ValAddressFromBech32(memberValoper)
	require.NoError(t, err)
	validator, err := f.Staking.GetValidator(f.Ctx, valAddr)
	require.NoError(t, err)
	require.Equal(t, math.NewInt(600), validator.Tokens) // 100 initial self-bond + 500 force-bonded
}

func TestDistributeEpochRewards_forceBondingStopsAtCeiling(t *testing.T) {
	f := newTestFixture(t)
	memberAddr := setupSingleCommitteeGenesis(t, f)
	memberValoper := sdk.ValAddress(memberAddr).String()
	f.Staking.addValidator(t, memberValoper, testPubKey(1), 1_900, "0.0")

	p, err := f.Keeper.Params.Get(f.Ctx)
	require.NoError(t, err)
	p.ForceBondFraction = math.LegacyNewDecWithPrec(50, 2)
	p.MinSelfBond = math.NewInt(1_000)
	p.SelfBondCapMultiplier = math.LegacyNewDec(2) // ceiling = 2,000; only 100 more norama fits
	require.NoError(t, f.Keeper.Params.Set(f.Ctx, p))

	f.Emission.epoch = 1
	f.Bank.fund(testEmissionModule, math.NewInt(1_000))
	_, err = f.Keeper.DistributeEpochRewards(f.Ctx, f.Emission, testEmissionModule, math.NewInt(1_000))
	require.NoError(t, err)

	// Wanted force-bond = 500, but only 100 norama of room remains under the 2,000 ceiling.
	require.Equal(t, math.NewInt(900), f.Earnings.credited[memberAddr.String()])
	selfBondTotal, err := f.Keeper.CommitteeSelfBond.Get(f.Ctx, memberValoper)
	require.NoError(t, err)
	require.Equal(t, math.NewInt(100), selfBondTotal)
}

func TestDistributeEpochRewards_delegatorsPaidProRata(t *testing.T) {
	f := newTestFixture(t)
	setupSingleCommitteeGenesis(t, f)

	outsiderValoper := sdk.ValAddress("outsider_with_delegat").String()
	f.Staking.addValidator(t, outsiderValoper, testPubKey(2), 700, "0.0") // self-delegation: 700
	valAddr, err := sdk.ValAddressFromBech32(outsiderValoper)
	require.NoError(t, err)
	validator, err := f.Staking.GetValidator(f.Ctx, valAddr)
	require.NoError(t, err)

	delegatorAddr := sdk.AccAddress("outside_delegator_one")
	_, err = f.Staking.Delegate(f.Ctx, delegatorAddr, math.NewInt(300), 0, validator, true)
	require.NoError(t, err)

	// Disable ramp so this validator's reward is visible in the same epoch it is set up.
	p, err := f.Keeper.Params.Get(f.Ctx)
	require.NoError(t, err)
	p.RampEpochs = 1
	p.BootstrapDeadlineEpochs = 1
	require.NoError(t, f.Keeper.Params.Set(f.Ctx, p))

	f.Emission.epoch = 1
	_, err = f.Keeper.RunEndBlock(f.Ctx, f.Emission) // stamp ramp activation at epoch 1
	require.NoError(t, err)
	// Deadline is 1 epoch, so the unconstrained lambda jumps to 1 on the next close.
	// The per-epoch voting-power limit spreads that jump across several closes.
	var lambda math.LegacyDec
	for epoch := uint64(2); epoch <= 8; epoch++ {
		f.Emission.epoch = epoch
		_, err = f.Keeper.RunEndBlock(f.Ctx, f.Emission)
		require.NoError(t, err)
		lambda, err = f.Keeper.Lambda.Get(f.Ctx)
		require.NoError(t, err)
		if lambda.Equal(math.LegacyOneDec()) {
			break
		}
	}
	require.True(t, lambda.Equal(math.LegacyOneDec()), "lambda = %s, want 1 before paying delegators", lambda)

	f.Bank.fund(testEmissionModule, math.NewInt(1_000))
	distributed, err := f.Keeper.DistributeEpochRewards(f.Ctx, f.Emission, testEmissionModule, math.NewInt(1_000))
	require.NoError(t, err)
	require.Equal(t, math.NewInt(1_000), distributed)

	// This validator is the only bonded stake, lambda=1, ramp complete: it gets the full 1,000.
	// 700/1000 tokens are the operator's own self-delegation, 300/1000 the outside delegator's:
	// 700 and 300 respectively (0% commission).
	operatorAcc := sdk.AccAddress(valAddr)
	require.Equal(t, math.NewInt(700), f.Earnings.credited[operatorAcc.String()])
	require.Equal(t, math.NewInt(300), f.Earnings.credited[delegatorAddr.String()])
	_ = params.BaseDenom
}

// A validator whose reward cannot be paid must not fail the epoch close: its share goes back to
// the source module, x/power's account still ends empty, and the failure is reported.
func TestDistributeEpochRewards_anUnpayableValidatorDoesNotHaltTheEpoch(t *testing.T) {
	f := newTestFixture(t)
	memberAddr := setupSingleCommitteeGenesis(t, f)
	// No force-bond: the fake bank does not roll back a transfer made on a discarded branch.
	p, err := f.Keeper.Params.Get(f.Ctx)
	require.NoError(t, err)
	p.ForceBondFraction = math.LegacyZeroDec()
	require.NoError(t, f.Keeper.Params.Set(f.Ctx, p))
	f.Emission.epoch = 1
	f.Bank.fund(testEmissionModule, math.NewInt(1_000))
	f.Earnings.failFor = memberAddr.String()

	distributed, err := f.Keeper.DistributeEpochRewards(f.Ctx, f.Emission, testEmissionModule, math.NewInt(1_000))
	require.NoError(t, err)
	require.True(t, distributed.IsZero(), "nothing was paid")
	require.Equal(t, math.NewInt(1_000), f.Bank.balanceOf(testEmissionModule), "the unpaid share is back in the source module")
	require.True(t, f.Bank.balanceOf("power").IsZero(), "x/power's account ends empty")
	require.Empty(t, f.Earnings.credited)
	var reported bool
	for _, e := range f.Ctx.EventManager().Events() {
		reported = reported || e.Type == "power_reward_failed"
	}
	require.True(t, reported)
}
