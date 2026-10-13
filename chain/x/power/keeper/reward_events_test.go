package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/power/types"
)

// eventsOf returns the attributes of every event of the given type, in emission order.
func eventsOf(f *testFixture, typ string) []map[string]string {
	var out []map[string]string
	for _, e := range f.Ctx.EventManager().Events() {
		if e.Type != typ {
			continue
		}
		attrs := map[string]string{}
		for _, a := range e.Attributes {
			attrs[a.Key] = a.Value
		}
		out = append(out, attrs)
	}
	return out
}

// payOutsiderWithDelegator sets up a lone full-power validator with a 700 self-delegation and one
// outside delegator of 300 at the given commission rate, then pays an epoch's 1,000.
func payOutsiderWithDelegator(t *testing.T, f *testFixture, commission string) (outsiderValoper string, delegator sdk.AccAddress) {
	t.Helper()
	setupSingleCommitteeGenesis(t, f)
	outsiderValoper = sdk.ValAddress("outsider_with_delegat").String()
	f.Staking.addValidator(t, outsiderValoper, testPubKey(2), 700, commission)
	valAddr, err := sdk.ValAddressFromBech32(outsiderValoper)
	require.NoError(t, err)
	validator, err := f.Staking.GetValidator(f.Ctx, valAddr)
	require.NoError(t, err)
	delegator = sdk.AccAddress("outside_delegator_one")
	_, err = f.Staking.Delegate(f.Ctx, delegator, math.NewInt(300), 0, validator, true)
	require.NoError(t, err)

	p, err := f.Keeper.Params.Get(f.Ctx)
	require.NoError(t, err)
	p.RampEpochs, p.BootstrapDeadlineEpochs = 1, 1
	require.NoError(t, f.Keeper.Params.Set(f.Ctx, p))
	f.Emission.epoch = 1
	_, err = f.Keeper.RunEndBlock(f.Ctx, f.Emission)
	require.NoError(t, err)
	for epoch := uint64(2); epoch <= 8; epoch++ {
		f.Emission.epoch = epoch
		_, err = f.Keeper.RunEndBlock(f.Ctx, f.Emission)
		require.NoError(t, err)
		if lambda, lerr := f.Keeper.Lambda.Get(f.Ctx); lerr == nil && lambda.Equal(math.LegacyOneDec()) {
			break
		}
	}
	f.Bank.fund(testEmissionModule, math.NewInt(1_000))
	_, err = f.Keeper.DistributeEpochRewards(f.Ctx, f.Emission, testEmissionModule, math.NewInt(1_000))
	require.NoError(t, err)
	return outsiderValoper, delegator
}

func TestDistributeEpochRewards_eventsAttributeTheSplitAtZeroCommission(t *testing.T) {
	f := newTestFixture(t)
	valoper, delegator := payOutsiderWithDelegator(t, f, "0.0")

	vals := eventsOf(f, types.EventTypeValidatorReward)
	require.Len(t, vals, 1)
	require.Equal(t, map[string]string{
		types.AttributeKeyValidator: valoper, types.AttributeKeyAmount: "1000", types.AttributeKeyCommission: "0",
		types.AttributeKeyOperatorShare: "700", types.AttributeKeyDelegators: "300", types.AttributeKeyForceBonded: "0",
	}, vals[0])
	dels := eventsOf(f, types.EventTypeDelegatorReward)
	require.Len(t, dels, 1)
	require.Equal(t, map[string]string{
		types.AttributeKeyValidator: valoper, types.AttributeKeyDelegator: delegator.String(), types.AttributeKeyAmount: "300",
	}, dels[0])
}

func TestDistributeEpochRewards_eventsSeparateCommissionFromTheDelegatorShare(t *testing.T) {
	f := newTestFixture(t)
	valoper, delegator := payOutsiderWithDelegator(t, f, "0.1")

	// 10% of 1,000 is commission; the other 900 is shared 700:300 -> 630 to the operator's own
	// delegation, 270 to the delegator.
	vals := eventsOf(f, types.EventTypeValidatorReward)
	require.Len(t, vals, 1)
	require.Equal(t, valoper, vals[0][types.AttributeKeyValidator])
	require.Equal(t, "100", vals[0][types.AttributeKeyCommission])
	require.Equal(t, "730", vals[0][types.AttributeKeyOperatorShare])
	require.Equal(t, "270", vals[0][types.AttributeKeyDelegators])
	dels := eventsOf(f, types.EventTypeDelegatorReward)
	require.Len(t, dels, 1)
	require.Equal(t, "270", dels[0][types.AttributeKeyAmount])
	require.Equal(t, math.NewInt(270), f.Earnings.credited[delegator.String()])
}

func TestDistributeEpochRewards_eventForAMemberWithNoDelegatorsCountsTheForceBond(t *testing.T) {
	f := newTestFixture(t)
	memberAddr := setupSingleCommitteeGenesis(t, f)
	f.Emission.epoch = 1
	f.Bank.fund(testEmissionModule, math.NewInt(1_000))

	_, err := f.Keeper.DistributeEpochRewards(f.Ctx, f.Emission, testEmissionModule, math.NewInt(1_000))
	require.NoError(t, err)

	vals := eventsOf(f, types.EventTypeValidatorReward)
	require.Len(t, vals, 1)
	require.Equal(t, sdk.ValAddress(memberAddr).String(), vals[0][types.AttributeKeyValidator])
	require.Equal(t, "1000", vals[0][types.AttributeKeyOperatorShare])
	require.Equal(t, "0", vals[0][types.AttributeKeyCommission])
	require.Equal(t, "0", vals[0][types.AttributeKeyDelegators])
	require.Equal(t, "500", vals[0][types.AttributeKeyForceBonded])
	require.Empty(t, eventsOf(f, types.EventTypeDelegatorReward), "no delegator was credited")
}

func TestDistributeEpochRewards_anUnpaidValidatorEmitsNoRewardEvent(t *testing.T) {
	f := newTestFixture(t)
	memberAddr := setupSingleCommitteeGenesis(t, f)
	p, err := f.Keeper.Params.Get(f.Ctx)
	require.NoError(t, err)
	p.ForceBondFraction = math.LegacyZeroDec()
	require.NoError(t, f.Keeper.Params.Set(f.Ctx, p))
	f.Emission.epoch = 1
	f.Bank.fund(testEmissionModule, math.NewInt(1_000))
	f.Earnings.failFor = memberAddr.String()

	_, err = f.Keeper.DistributeEpochRewards(f.Ctx, f.Emission, testEmissionModule, math.NewInt(1_000))
	require.NoError(t, err)

	require.Empty(t, eventsOf(f, types.EventTypeValidatorReward), "a reward that was rolled back is not reported as paid")
	require.Len(t, eventsOf(f, "power_reward_failed"), 1)
}

func TestDistributeEpochRewards_noRewardEventsWithoutAMint(t *testing.T) {
	f := newTestFixture(t)
	setupSingleCommitteeGenesis(t, f)
	f.Emission.epoch = 1

	distributed, err := f.Keeper.DistributeEpochRewards(f.Ctx, f.Emission, testEmissionModule, math.ZeroInt())
	require.NoError(t, err)
	require.True(t, distributed.IsZero())
	require.Empty(t, eventsOf(f, types.EventTypeValidatorReward))
}
