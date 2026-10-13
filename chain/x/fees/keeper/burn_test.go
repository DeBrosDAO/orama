package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/fees/types"
)

func TestBurnFromBalance_destroysTheAmountAndLeavesNothingInTheModule(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	from := sdk.AccAddress("burn_payer___________")
	f.Bank.fund(from.String(), math.NewInt(1_000))

	require.NoError(t, f.Keeper.BurnFromBalance(f.Ctx, from, math.NewInt(400), "a test fee"))

	require.True(t, f.Bank.balanceOf(from.String()).Equal(math.NewInt(600)))
	require.True(t, f.Bank.balanceOf(types.ModuleName).IsZero(), "burned, not parked in x/fees")
}

func TestBurnFromBalance_refusesWithoutTouchingEarningsOrBalance(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	from := sdk.AccAddress("burn_poor_payer______")
	f.Bank.fund(from.String(), math.NewInt(100))
	f.Bank.fund(testSourceModule, math.NewInt(900))
	require.NoError(t, f.Keeper.CreditEarnings(f.Ctx, testSourceModule, from, sdk.NewCoin(params.BaseDenom, math.NewInt(900))))

	err := f.Keeper.BurnFromBalance(f.Ctx, from, math.NewInt(400), "a test fee")
	require.ErrorIs(t, err, sdkerrors.ErrInsufficientFunds, "earnings do not fund a burn")

	require.True(t, f.Bank.balanceOf(from.String()).Equal(math.NewInt(100)))
	earned, err := f.Keeper.GetEarnings(f.Ctx, from)
	require.NoError(t, err)
	require.True(t, earned.Equal(math.NewInt(900)))
}

func TestBurnFromBalance_rejectsNonPositiveAmounts(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	from := sdk.AccAddress("burn_zero_payer______")
	require.Error(t, f.Keeper.BurnFromBalance(f.Ctx, from, math.ZeroInt(), "a test fee"))
	require.Error(t, f.Keeper.BurnFromBalance(f.Ctx, from, math.Int{}, "a test fee"))
}
