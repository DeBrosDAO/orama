package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
)

func requireEarnings(t *testing.T, f *testFixture, addr sdk.AccAddress, want int64) {
	t.Helper()
	got, err := f.Keeper.GetEarnings(f.Ctx, addr)
	require.NoError(t, err)
	require.True(t, got.Equal(math.NewInt(want)), "earnings = %s, want %d", got, want)
	inv, err := f.Keeper.CheckInvariants(f.Ctx)
	require.NoError(t, err)
	require.True(t, inv.EarningsMatchModule, inv.Detail)
}

func fundEarnings(t *testing.T, f *testFixture, addr sdk.AccAddress, bank, earnings int64) {
	t.Helper()
	if bank > 0 {
		f.Bank.fund(addr.String(), math.NewInt(bank))
	}
	f.Bank.fund(testSourceModule, math.NewInt(earnings))
	require.NoError(t, f.Keeper.CreditEarnings(f.Ctx, testSourceModule, addr, sdk.NewCoin(params.BaseDenom, math.NewInt(earnings))))
}

func TestFundSpendFromEarnings_fundsTheShortfall(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	addr := sdk.AccAddress("outsider_bonder_____")
	fundEarnings(t, f, addr, 100, 900)

	require.NoError(t, f.Keeper.FundSpendFromEarnings(f.Ctx, addr, params.BaseDenom, math.NewInt(400)))
	require.True(t, f.Bank.balanceOf(addr.String()).Equal(math.NewInt(400)), "bank had 100, so 300 comes from earnings")
	requireEarnings(t, f, addr, 600)
}

func TestFundSpendFromEarnings_leavesACoveredBondAlone(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	addr := sdk.AccAddress("already_funded_bond_")
	fundEarnings(t, f, addr, 500, 500)

	require.NoError(t, f.Keeper.FundSpendFromEarnings(f.Ctx, addr, params.BaseDenom, math.NewInt(500)))
	requireEarnings(t, f, addr, 500)
	require.True(t, f.Bank.balanceOf(addr.String()).Equal(math.NewInt(500)))
}

func TestFundSpendFromEarnings_insufficientEarningsMovesNothing(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	addr := sdk.AccAddress("short_of_earnings___")
	fundEarnings(t, f, addr, 100, 200)

	require.NoError(t, f.Keeper.FundSpendFromEarnings(f.Ctx, addr, params.BaseDenom, math.NewInt(1000)))
	requireEarnings(t, f, addr, 200)
	require.True(t, f.Bank.balanceOf(addr.String()).Equal(math.NewInt(100)), "a partial top-up cannot make the bond succeed, so none is made")
}

func TestFundSpendFromEarnings_neverTouchesAnotherAddress(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	rich := sdk.AccAddress("rich_in_earnings____")
	poor := sdk.AccAddress("poor_in_earnings____")
	fundEarnings(t, f, rich, 0, 10_000)

	require.NoError(t, f.Keeper.FundSpendFromEarnings(f.Ctx, poor, params.BaseDenom, math.NewInt(1000)))
	requireEarnings(t, f, rich, 10_000)
	require.True(t, f.Bank.balanceOf(poor.String()).IsZero())
}

func TestFundSpendFromEarnings_emptyAmountIsANoop(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	addr := sdk.AccAddress("noop_amount_________")
	fundEarnings(t, f, addr, 0, 100)

	require.NoError(t, f.Keeper.FundSpendFromEarnings(f.Ctx, addr, params.BaseDenom, math.ZeroInt()))
	require.NoError(t, f.Keeper.FundSpendFromEarnings(f.Ctx, addr, params.BaseDenom, math.Int{}))
	require.NoError(t, f.Keeper.FundSpendFromEarnings(f.Ctx, addr, params.BaseDenom, math.NewInt(-5)))
	requireEarnings(t, f, addr, 100)
}

// The message handler that calls FundSpendFromEarnings runs in the message's own cache branch. When
// the message fails afterwards, that branch is dropped, and so is the top-up.
func TestFundSpendFromEarnings_isDiscardedWithAFailedMessageBranch(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	addr := sdk.AccAddress("failing_message_____")
	fundEarnings(t, f, addr, 0, 500)

	msgCtx, _ := f.Ctx.CacheContext() // the branch runTx gives runMsgs; it is not written when the message fails.
	require.NoError(t, f.Keeper.FundSpendFromEarnings(msgCtx, addr, params.BaseDenom, math.NewInt(400)))
	got, err := f.Keeper.GetEarnings(msgCtx, addr)
	require.NoError(t, err)
	require.True(t, got.Equal(math.NewInt(100)))

	// The fake bank is an in-memory map and does not branch, so only the store-backed ledger is
	// checked here; the real bank's discard is covered by the app tests.
	after, err := f.Keeper.GetEarnings(f.Ctx, addr)
	require.NoError(t, err)
	require.True(t, after.Equal(math.NewInt(500)))
}

// Earnings are norama only: a top-up asked for any other denom moves nothing, so an earnings
// balance can never be paid out as a coin it is not.
func TestFundSpendFromEarnings_onlyTheBaseDenomIsFunded(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	addr := sdk.AccAddress("other_denom_bonder__")
	fundEarnings(t, f, addr, 0, 900)

	require.NoError(t, f.Keeper.FundSpendFromEarnings(f.Ctx, addr, "ustake", math.NewInt(400)))
	moved, err := f.Keeper.TopUpSpendFromEarnings(f.Ctx, addr, "ustake", math.NewInt(400))
	require.NoError(t, err)
	require.True(t, moved.IsZero())
	require.True(t, f.Bank.balanceOf(addr.String()).IsZero(), "no coin reached the bank balance")
	requireEarnings(t, f, addr, 900)

	require.NoError(t, f.Keeper.FundSpendFromEarnings(f.Ctx, addr, params.BaseDenom, math.NewInt(400)))
	require.True(t, f.Bank.balanceOf(addr.String()).Equal(math.NewInt(400)), "the base denom is still funded")
	requireEarnings(t, f, addr, 500)
}
