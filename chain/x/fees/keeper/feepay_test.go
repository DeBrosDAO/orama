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
