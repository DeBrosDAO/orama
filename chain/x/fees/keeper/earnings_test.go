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
