package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
)

func TestSettleFee_paysFromBankAndBurnsBaseFee(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	payer := sdk.AccAddress("fee_payer_one________")
	proposer := sdk.AccAddress("proposer_one_________")
	f.Bank.fund(payer.String(), math.NewInt(1_300))

	require.NoError(t, f.Keeper.SettleFee(f.Ctx, payer, proposer, math.NewInt(700), math.NewInt(300), true))

	require.True(t, f.Bank.balanceOf(payer.String()).Equal(math.NewInt(300)), "the full 1,000 fee (base+tip) deducted, 300 left of the 1,300 funded")
	require.True(t, f.Bank.burned.Equal(math.NewInt(700)), "the base fee is burned in full")

	tipBalance, err := f.Keeper.GetEarnings(f.Ctx, proposer)
	require.NoError(t, err)
	require.True(t, tipBalance.Equal(math.NewInt(300)), "the tip is credited to the proposer's earnings")
}

func TestSettleFee_baseFeeFallsBackToEarningsWhenBankIsShort(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	payer := sdk.AccAddress("fee_payer_two________")
	proposer := sdk.AccAddress("proposer_two_________")

	// Payer has 400 in the bank (enough for the 300 tip plus 100 of the 700 base fee) and 600 in
	// earnings (e.g. credited by x/power earlier) to cover the rest of the base fee.
	f.Bank.fund(payer.String(), math.NewInt(400))
	f.Bank.fund(testSourceModule, math.NewInt(600))
	require.NoError(t, f.Keeper.CreditEarnings(f.Ctx, testSourceModule, payer, sdk.NewCoin(params.BaseDenom, math.NewInt(600))))

	require.NoError(t, f.Keeper.SettleFee(f.Ctx, payer, proposer, math.NewInt(700), math.NewInt(300), true))

	require.True(t, f.Bank.balanceOf(payer.String()).IsZero(), "the full 400 bank balance is used first")
	payerEarnings, err := f.Keeper.GetEarnings(f.Ctx, payer)
	require.NoError(t, err)
	require.True(t, payerEarnings.IsZero(), "the remaining 600 in earnings covers the rest of the base fee")

	require.True(t, f.Bank.burned.Equal(math.NewInt(700)))
	proposerEarnings, err := f.Keeper.GetEarnings(f.Ctx, proposer)
	require.NoError(t, err)
	require.True(t, proposerEarnings.Equal(math.NewInt(300)))
}

func TestSettleFee_tipMustComeFromBankNeverEarnings(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	payer := sdk.AccAddress("fee_payer_tip________")
	proposer := sdk.AccAddress("proposer_tip_________")

	// Payer has plenty in earnings but only 100 in the bank - not enough to cover a 300 tip, even
	// though earnings could easily cover the whole fee otherwise (security review M4).
	f.Bank.fund(payer.String(), math.NewInt(100))
	f.Bank.fund(testSourceModule, math.NewInt(10_000))
	require.NoError(t, f.Keeper.CreditEarnings(f.Ctx, testSourceModule, payer, sdk.NewCoin(params.BaseDenom, math.NewInt(10_000))))

	err := f.Keeper.SettleFee(f.Ctx, payer, proposer, math.NewInt(700), math.NewInt(300), true)
	require.Error(t, err)
	require.Contains(t, err.Error(), "tip")
}

func TestSettleFee_feeGranterCannotUseEarningsForBase(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	granter := sdk.AccAddress("fee_granter__________")
	proposer := sdk.AccAddress("proposer_granter_____")

	// The granter has enough to cover the tip, but not the full base fee, and has earnings that
	// COULD cover the rest - but a fee granter must never draw on anyone's earnings (security
	// review, non-blocking "fee granter").
	f.Bank.fund(granter.String(), math.NewInt(400))
	f.Bank.fund(testSourceModule, math.NewInt(10_000))
	require.NoError(t, f.Keeper.CreditEarnings(f.Ctx, testSourceModule, granter, sdk.NewCoin(params.BaseDenom, math.NewInt(10_000))))

	err := f.Keeper.SettleFee(f.Ctx, granter, proposer, math.NewInt(700), math.NewInt(300), false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "fee granter")

	// Nothing should have moved.
	granterEarnings, err := f.Keeper.GetEarnings(f.Ctx, granter)
	require.NoError(t, err)
	require.True(t, granterEarnings.Equal(math.NewInt(10_000)))
}

func TestSettleFee_failsWhenNeitherBankNorEarningsCoverIt(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	payer := sdk.AccAddress("fee_payer_three______")
	proposer := sdk.AccAddress("proposer_three_______")
	f.Bank.fund(payer.String(), math.NewInt(310))

	err := f.Keeper.SettleFee(f.Ctx, payer, proposer, math.NewInt(700), math.NewInt(300), true)
	require.Error(t, err)
}

func TestSettleFee_zeroFeeIsANoOp(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	payer := sdk.AccAddress("fee_payer_four_______")
	proposer := sdk.AccAddress("proposer_four________")
	require.NoError(t, f.Keeper.SettleFee(f.Ctx, payer, proposer, math.ZeroInt(), math.ZeroInt(), true))
}

func fundFeeBalance(t *testing.T, f *testFixture, hot sdk.AccAddress, amount int64) {
	t.Helper()
	operator := sdk.AccAddress("operator_of_hot_key_")
	fundedEarnings(t, f, operator, amount)
	require.NoError(t, f.Keeper.FundFeeBalance(f.Ctx, operator, hot, math.NewInt(amount)))
}

func TestSettleFee_hotKeyPaysTheBaseFeeFromItsFeeBalance(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	hot := sdk.AccAddress("hot_key_pays_base____")
	proposer := sdk.AccAddress("proposer_hot_________")
	fundFeeBalance(t, f, hot, 1000)

	require.NoError(t, f.Keeper.SettleFee(f.Ctx, hot, proposer, math.NewInt(600), math.ZeroInt(), true))

	left, err := f.Keeper.GetFeeBalance(f.Ctx, hot)
	require.NoError(t, err)
	require.True(t, left.Equal(math.NewInt(400)))
	require.True(t, f.Bank.burned.Equal(math.NewInt(600)))
	inv, err := f.Keeper.CheckInvariants(f.Ctx)
	require.NoError(t, err)
	require.True(t, inv.EarningsMatchModule, inv.Detail)
	require.True(t, inv.FeesBalance, inv.Detail)
}

func TestSettleFee_feeBalanceNeverPaysATip(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	hot := sdk.AccAddress("hot_key_tip_attempt__")
	proposer := sdk.AccAddress("proposer_tip__________")
	fundFeeBalance(t, f, hot, 1000)

	require.Error(t, f.Keeper.SettleFee(f.Ctx, hot, proposer, math.NewInt(100), math.NewInt(50), true),
		"a tip is a public payment and must come from a bank balance")
	tip, err := f.Keeper.GetEarnings(f.Ctx, proposer)
	require.NoError(t, err)
	require.True(t, tip.IsZero())
}

func TestSettleFee_feeBalanceIsNotUsedThroughAFeeGranter(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	hot := sdk.AccAddress("hot_key_via_granter_")
	proposer := sdk.AccAddress("proposer_granter_____")
	fundFeeBalance(t, f, hot, 1000)

	require.Error(t, f.Keeper.SettleFee(f.Ctx, hot, proposer, math.NewInt(100), math.ZeroInt(), false))
	left, err := f.Keeper.GetFeeBalance(f.Ctx, hot)
	require.NoError(t, err)
	require.True(t, left.Equal(math.NewInt(1000)))
}

func TestSettleFee_feeBalanceThenEarningsCoverTheBaseFee(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	hot := sdk.AccAddress("hot_key_mixed________")
	proposer := sdk.AccAddress("proposer_mixed_______")
	fundFeeBalance(t, f, hot, 300)
	fundedEarnings(t, f, hot, 500)

	require.NoError(t, f.Keeper.SettleFee(f.Ctx, hot, proposer, math.NewInt(600), math.ZeroInt(), true))

	fee, err := f.Keeper.GetFeeBalance(f.Ctx, hot)
	require.NoError(t, err)
	require.True(t, fee.IsZero(), "the fee balance is spent first")
	earned, err := f.Keeper.GetEarnings(f.Ctx, hot)
	require.NoError(t, err)
	require.True(t, earned.Equal(math.NewInt(200)), "earnings cover the remaining 300")
	inv, err := f.Keeper.CheckInvariants(f.Ctx)
	require.NoError(t, err)
	require.True(t, inv.EarningsMatchModule, inv.Detail)
}

func TestSettleFee_insufficientFeeBalanceAndEarningsFails(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	hot := sdk.AccAddress("hot_key_too_poor_____")
	proposer := sdk.AccAddress("proposer_poor________")
	fundFeeBalance(t, f, hot, 100)

	require.Error(t, f.Keeper.SettleFee(f.Ctx, hot, proposer, math.NewInt(600), math.ZeroInt(), true))
}
