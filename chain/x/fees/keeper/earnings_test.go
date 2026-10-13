package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/fees/types"
)

const testSourceModule = "emission"

func TestCreditEarnings_movesCoinsAndUpdatesLedger(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	f.Bank.fund(testSourceModule, math.NewInt(500))

	addr := sdk.AccAddress("recipient_one________")
	require.NoError(t, f.Keeper.CreditEarnings(f.Ctx, testSourceModule, addr, sdk.NewCoin(params.BaseDenom, math.NewInt(500))))

	balance, err := f.Keeper.GetEarnings(f.Ctx, addr)
	require.NoError(t, err)
	require.True(t, (balance).Equal(math.NewInt(500)))
	require.True(t, (f.Bank.balanceOf(types.ModuleName).Equal(math.NewInt(500))))
	require.True(t, (f.Bank.balanceOf(testSourceModule).Equal(math.ZeroInt())))
}

func TestCreditEarnings_accumulatesAcrossCalls(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	addr := sdk.AccAddress("recipient_two________")
	f.Bank.fund(testSourceModule, math.NewInt(1_000))

	require.NoError(t, f.Keeper.CreditEarnings(f.Ctx, testSourceModule, addr, sdk.NewCoin(params.BaseDenom, math.NewInt(300))))
	require.NoError(t, f.Keeper.CreditEarnings(f.Ctx, testSourceModule, addr, sdk.NewCoin(params.BaseDenom, math.NewInt(200))))

	balance, err := f.Keeper.GetEarnings(f.Ctx, addr)
	require.NoError(t, err)
	require.True(t, (balance).Equal(math.NewInt(500)))
}

func TestDebitEarningsUpTo_neverExceedsBalance(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	addr := sdk.AccAddress("recipient_three______")
	f.Bank.fund(testSourceModule, math.NewInt(100))
	require.NoError(t, f.Keeper.CreditEarnings(f.Ctx, testSourceModule, addr, sdk.NewCoin(params.BaseDenom, math.NewInt(100))))

	debited, err := f.Keeper.DebitEarningsUpTo(f.Ctx, addr, math.NewInt(1_000))
	require.NoError(t, err)
	require.True(t, (debited).Equal(math.NewInt(100)), "debit should be capped at the actual balance")

	balance, err := f.Keeper.GetEarnings(f.Ctx, addr)
	require.NoError(t, err)
	require.True(t, balance.IsZero())

	has, err := f.Keeper.Earnings.Has(f.Ctx, addr.String())
	require.NoError(t, err)
	require.False(t, has, "a balance debited back to zero must remove the key, not store a zero")
}

func TestGetEarnings_zeroForUnknownAddress(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	balance, err := f.Keeper.GetEarnings(f.Ctx, sdk.AccAddress("nobody_______________"))
	require.NoError(t, err)
	require.True(t, balance.IsZero())
}

func fundedEarnings(t *testing.T, f *testFixture, addr sdk.AccAddress, amount int64) {
	t.Helper()
	f.Bank.fund(testSourceModule, math.NewInt(amount))
	require.NoError(t, f.Keeper.CreditEarnings(f.Ctx, testSourceModule, addr, sdk.NewCoin(params.BaseDenom, math.NewInt(amount))))
}

func TestFundFeeBalance_movesLedgerWithoutMovingCoins(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	from := sdk.AccAddress("move_from____________")
	to := sdk.AccAddress("move_to______________")
	fundedEarnings(t, f, from, 500)

	require.NoError(t, f.Keeper.FundFeeBalance(f.Ctx, from, to, math.NewInt(200)))

	got, err := f.Keeper.GetEarnings(f.Ctx, from)
	require.NoError(t, err)
	require.True(t, got.Equal(math.NewInt(300)))
	got, err = f.Keeper.GetFeeBalance(f.Ctx, to)
	require.NoError(t, err)
	require.True(t, got.Equal(math.NewInt(200)))
	earned, err := f.Keeper.GetEarnings(f.Ctx, to)
	require.NoError(t, err)
	require.True(t, earned.IsZero(), "the target gets a fee-only balance, never earnings it could bond or shield")
	require.True(t, f.Bank.balanceOf(types.ModuleName).Equal(math.NewInt(500)), "coins stay in the fees module account")
	inv, err := f.Keeper.CheckInvariants(f.Ctx)
	require.NoError(t, err)
	require.True(t, inv.EarningsMatchModule, inv.Detail)
}

func TestFundFeeBalance_fullBalanceClearsSourceEntry(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	from := sdk.AccAddress("move_from____________")
	to := sdk.AccAddress("move_to______________")
	fundedEarnings(t, f, from, 100)

	require.NoError(t, f.Keeper.FundFeeBalance(f.Ctx, from, to, math.NewInt(100)))

	has, err := f.Keeper.Earnings.Has(f.Ctx, from.String())
	require.NoError(t, err)
	require.False(t, has, "a zero balance must not be left as a row")
}

func TestFundFeeBalance_refusesOverdraftZeroAndSelf(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	from := sdk.AccAddress("move_from____________")
	to := sdk.AccAddress("move_to______________")
	fundedEarnings(t, f, from, 100)

	require.Error(t, f.Keeper.FundFeeBalance(f.Ctx, from, to, math.NewInt(101)))
	require.Error(t, f.Keeper.FundFeeBalance(f.Ctx, from, to, math.ZeroInt()))
	require.Error(t, f.Keeper.FundFeeBalance(f.Ctx, from, to, math.NewInt(-1)))
	require.Error(t, f.Keeper.FundFeeBalance(f.Ctx, from, from, math.NewInt(1)))

	got, err := f.Keeper.GetEarnings(f.Ctx, from)
	require.NoError(t, err)
	require.True(t, got.Equal(math.NewInt(100)), "a refused move leaves the source untouched")
}

// A fee balance is not earnings: nothing that spends earnings (a bond top-up) reaches it.
func TestFeeBalance_cannotBeBonded(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	operator := sdk.AccAddress("operator_funding_hot")
	hot := sdk.AccAddress("hot_key_of_the_node_")
	fundedEarnings(t, f, operator, 1000)
	require.NoError(t, f.Keeper.FundFeeBalance(f.Ctx, operator, hot, math.NewInt(1000)))

	require.NoError(t, f.Keeper.FundSpendFromEarnings(f.Ctx, hot, params.BaseDenom, math.NewInt(500)))
	require.True(t, f.Bank.balanceOf(hot.String()).IsZero(), "a hot key's fee balance must never become a spendable bank balance")
	moved, err := f.Keeper.TopUpSpendFromEarnings(f.Ctx, hot, params.BaseDenom, math.NewInt(500))
	require.NoError(t, err)
	require.True(t, moved.IsZero())
	fee, err := f.Keeper.GetFeeBalance(f.Ctx, hot)
	require.NoError(t, err)
	require.True(t, fee.Equal(math.NewInt(1000)))
}

func TestPayEarnings_creditsTheRecipientsEarningsNotTheirBalance(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	payer := sdk.AccAddress("earnings_payer_______")
	recipient := sdk.AccAddress("earnings_recipient___")
	f.Bank.fund(payer.String(), math.NewInt(700))

	require.NoError(t, f.Keeper.PayEarnings(f.Ctx, payer, recipient, sdk.NewCoin(params.BaseDenom, math.NewInt(500))))

	require.True(t, f.Bank.balanceOf(payer.String()).Equal(math.NewInt(200)))
	require.True(t, f.Bank.balanceOf(recipient.String()).IsZero(), "a user never receives a public balance")
	got, err := f.Keeper.GetEarnings(f.Ctx, recipient)
	require.NoError(t, err)
	require.True(t, got.Equal(math.NewInt(500)))
	inv, err := f.Keeper.CheckInvariants(f.Ctx)
	require.NoError(t, err)
	require.True(t, inv.EarningsMatchModule, inv.Detail)
}

func TestPayEarnings_rejectsZero(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	payer := sdk.AccAddress("earnings_payer2______")
	recipient := sdk.AccAddress("earnings_recipient2__")
	f.Bank.fund(payer.String(), math.NewInt(10))

	require.Error(t, f.Keeper.PayEarnings(f.Ctx, payer, recipient, sdk.NewCoin(params.BaseDenom, math.ZeroInt())))
	got, err := f.Keeper.GetEarnings(f.Ctx, recipient)
	require.NoError(t, err)
	require.True(t, got.IsZero())
}

func TestCreditFeeBalance_movesCoinsIntoTheFeesAccountAsFeeOnlyMoney(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	f.Bank.fund(testSourceModule, math.NewInt(400))
	to := sdk.AccAddress("credit_to____________")

	require.NoError(t, f.Keeper.CreditFeeBalance(f.Ctx, testSourceModule, to, sdk.NewCoin(params.BaseDenom, math.NewInt(400))))

	got, err := f.Keeper.GetFeeBalance(f.Ctx, to)
	require.NoError(t, err)
	require.True(t, got.Equal(math.NewInt(400)))
	earned, err := f.Keeper.GetEarnings(f.Ctx, to)
	require.NoError(t, err)
	require.True(t, earned.IsZero(), "it is not earnings: nothing can bond or shield it")
	require.True(t, f.Bank.balanceOf(types.ModuleName).Equal(math.NewInt(400)))
	require.True(t, f.Bank.balanceOf(testSourceModule).IsZero())
	inv, err := f.Keeper.CheckInvariants(f.Ctx)
	require.NoError(t, err)
	require.True(t, inv.EarningsMatchModule, inv.Detail)
}

func TestCreditFeeBalance_refusesNonPositiveCredits(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	to := sdk.AccAddress("credit_to____________")
	require.Error(t, f.Keeper.CreditFeeBalance(f.Ctx, testSourceModule, to, sdk.NewCoin(params.BaseDenom, math.ZeroInt())))
	got, err := f.Keeper.GetFeeBalance(f.Ctx, to)
	require.NoError(t, err)
	require.True(t, got.IsZero())
}

func TestFundFeeBalanceFromBank_movesCoinsIntoTheFeesAccountAsFeeOnlyMoney(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	from := sdk.AccAddress("bank_from____________")
	to := sdk.AccAddress("bank_to______________")
	f.Bank.fund(from.String(), math.NewInt(900))

	require.NoError(t, f.Keeper.FundFeeBalanceFromBank(f.Ctx, from, to, math.NewInt(400)))

	got, err := f.Keeper.GetFeeBalance(f.Ctx, to)
	require.NoError(t, err)
	require.True(t, got.Equal(math.NewInt(400)))
	earned, err := f.Keeper.GetEarnings(f.Ctx, to)
	require.NoError(t, err)
	require.True(t, earned.IsZero(), "it is not earnings: nothing can bond or shield it")
	fromEarnings, err := f.Keeper.GetEarnings(f.Ctx, from)
	require.NoError(t, err)
	require.True(t, fromEarnings.IsZero(), "the source's earnings ledger is not touched")
	require.True(t, f.Bank.balanceOf(from.String()).Equal(math.NewInt(500)))
	require.True(t, f.Bank.balanceOf(types.ModuleName).Equal(math.NewInt(400)))
	inv, err := f.Keeper.CheckInvariants(f.Ctx)
	require.NoError(t, err)
	require.True(t, inv.EarningsMatchModule, inv.Detail)
}

func TestFundFeeBalanceFromBank_invariantHoldsAlongsideEarningsAndRepeatedFundings(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	earner := sdk.AccAddress("earner______________")
	from := sdk.AccAddress("bank_from____________")
	to := sdk.AccAddress("bank_to______________")
	fundedEarnings(t, f, earner, 300)
	f.Bank.fund(from.String(), math.NewInt(1_000))

	require.NoError(t, f.Keeper.FundFeeBalanceFromBank(f.Ctx, from, to, math.NewInt(100)))
	require.NoError(t, f.Keeper.FundFeeBalanceFromBank(f.Ctx, from, to, math.NewInt(250)))
	require.NoError(t, f.Keeper.FundFeeBalance(f.Ctx, earner, to, math.NewInt(50)))

	got, err := f.Keeper.GetFeeBalance(f.Ctx, to)
	require.NoError(t, err)
	require.True(t, got.Equal(math.NewInt(400)))
	require.True(t, f.Bank.balanceOf(types.ModuleName).Equal(math.NewInt(650)), "300 earned plus 350 from the bank")
	inv, err := f.Keeper.CheckInvariants(f.Ctx)
	require.NoError(t, err)
	require.True(t, inv.EarningsMatchModule, inv.Detail)
}

func TestFundFeeBalanceFromBank_refusesOverdraftZeroAndSelf(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	from := sdk.AccAddress("bank_from____________")
	to := sdk.AccAddress("bank_to______________")
	f.Bank.fund(from.String(), math.NewInt(100))

	require.Error(t, f.Keeper.FundFeeBalanceFromBank(f.Ctx, from, to, math.NewInt(101)))
	require.Error(t, f.Keeper.FundFeeBalanceFromBank(f.Ctx, from, to, math.ZeroInt()))
	require.Error(t, f.Keeper.FundFeeBalanceFromBank(f.Ctx, from, to, math.NewInt(-1)))
	require.Error(t, f.Keeper.FundFeeBalanceFromBank(f.Ctx, from, to, math.Int{}))
	require.Error(t, f.Keeper.FundFeeBalanceFromBank(f.Ctx, from, from, math.NewInt(1)))

	require.True(t, f.Bank.balanceOf(from.String()).Equal(math.NewInt(100)), "a refused funding leaves the bank balance untouched")
	require.True(t, f.Bank.balanceOf(types.ModuleName).IsZero())
	got, err := f.Keeper.GetFeeBalance(f.Ctx, to)
	require.NoError(t, err)
	require.True(t, got.IsZero())
}
