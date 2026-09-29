package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/fees/types"
)

func TestLockDeposit_movesFundsAndRecordsEntry(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	owner := sdk.AccAddress("deposit_owner________")
	f.Bank.fund(owner.String(), math.NewInt(1_000))

	require.NoError(t, f.Keeper.LockDeposit(f.Ctx, owner, "storage/deal/1", math.NewInt(1_000)))

	require.True(t, f.Bank.balanceOf(owner.String()).Equal(math.ZeroInt()))
	require.True(t, (f.Bank.balanceOf(types.DepositsModuleName).Equal(math.NewInt(1_000))))

	d, err := f.Keeper.GetDeposit(f.Ctx, "storage/deal/1")
	require.NoError(t, err)
	require.Equal(t, owner.String(), d.Owner)
	require.True(t, (d.Amount).Equal(math.NewInt(1_000)))
}

func TestLockDeposit_drawsTheShortfallFromEarnings(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	owner := sdk.AccAddress("deposit_owner_earn__")
	f.Bank.fund(owner.String(), math.NewInt(400))
	f.Bank.fund(testSourceModule, math.NewInt(600))
	require.NoError(t, f.Keeper.CreditEarnings(f.Ctx, testSourceModule, owner, sdk.NewCoin(params.BaseDenom, math.NewInt(600))))

	require.NoError(t, f.Keeper.LockDeposit(f.Ctx, owner, "storage/deal/earn", math.NewInt(1_000)))

	require.True(t, f.Bank.balanceOf(owner.String()).IsZero())
	balance, err := f.Keeper.GetEarnings(f.Ctx, owner)
	require.NoError(t, err)
	require.True(t, balance.IsZero())
	require.True(t, f.Bank.balanceOf(types.DepositsModuleName).Equal(math.NewInt(1_000)))
	require.True(t, f.Bank.balanceOf(types.ModuleName).IsZero())

	got, err := f.Keeper.CheckInvariants(f.Ctx)
	require.NoError(t, err)
	require.True(t, got.EarningsMatchModule, got.Detail)
	require.True(t, got.DepositsMatchModule, got.Detail)
	require.True(t, got.FeesBalance, got.Detail)
}

func TestLockDeposit_rejectsDuplicateID(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	owner := sdk.AccAddress("deposit_owner2_______")
	f.Bank.fund(owner.String(), math.NewInt(2_000))

	require.NoError(t, f.Keeper.LockDeposit(f.Ctx, owner, "dup", math.NewInt(1_000)))
	require.Error(t, f.Keeper.LockDeposit(f.Ctx, owner, "dup", math.NewInt(1_000)))
}

func TestReleaseDeposit_refundsAndBurnsExactSplit(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	owner := sdk.AccAddress("deposit_owner3_______")
	f.Bank.fund(owner.String(), math.NewInt(1_000))
	require.NoError(t, f.Keeper.LockDeposit(f.Ctx, owner, "release-me", math.NewInt(1_000)))

	refund, burn, err := f.Keeper.ReleaseDeposit(f.Ctx, "release-me")
	require.NoError(t, err)
	require.True(t, (refund).Equal(math.NewInt(990)))
	require.True(t, (burn).Equal(math.NewInt(10)))

	balance, err := f.Keeper.GetEarnings(f.Ctx, owner)
	require.NoError(t, err)
	require.True(t, (balance).Equal(math.NewInt(990)))

	require.True(t, (f.Bank.burned).Equal(math.NewInt(10)))
	require.True(t, f.Bank.balanceOf(types.DepositsModuleName).IsZero())

	_, err = f.Keeper.GetDeposit(f.Ctx, "release-me")
	require.Error(t, err, "released deposit should no longer exist")
}

func TestReleaseDeposit_unknownIDFails(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	_, _, err := f.Keeper.ReleaseDeposit(f.Ctx, "does-not-exist")
	require.Error(t, err)
}

func TestReleaseDepositPart_refundsPartAndKeepsRemainder(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	owner := sdk.AccAddress("deposit_owner_part___")
	f.Bank.fund(owner.String(), math.NewInt(1_000))
	require.NoError(t, f.Keeper.LockDeposit(f.Ctx, owner, "part", math.NewInt(1_000)))

	refund, burn, err := f.Keeper.ReleaseDepositPart(f.Ctx, "part", math.NewInt(400))
	require.NoError(t, err)
	require.True(t, refund.Equal(math.NewInt(396)))
	require.True(t, burn.Equal(math.NewInt(4)))

	d, err := f.Keeper.GetDeposit(f.Ctx, "part")
	require.NoError(t, err)
	require.True(t, d.Amount.Equal(math.NewInt(600)))
	require.True(t, f.Bank.balanceOf(types.DepositsModuleName).Equal(math.NewInt(600)))

	got, err := f.Keeper.CheckInvariants(f.Ctx)
	require.NoError(t, err)
	require.True(t, got.EarningsMatchModule && got.DepositsMatchModule, got.Detail)
}

func TestReleaseDepositPart_rejectsWholeOrZeroOrUnknown(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	owner := sdk.AccAddress("deposit_owner_part2__")
	f.Bank.fund(owner.String(), math.NewInt(1_000))
	require.NoError(t, f.Keeper.LockDeposit(f.Ctx, owner, "part2", math.NewInt(1_000)))

	_, _, err := f.Keeper.ReleaseDepositPart(f.Ctx, "part2", math.NewInt(1_000))
	require.Error(t, err, "a whole release belongs to ReleaseDeposit")
	_, _, err = f.Keeper.ReleaseDepositPart(f.Ctx, "part2", math.ZeroInt())
	require.Error(t, err)
	_, _, err = f.Keeper.ReleaseDepositPart(f.Ctx, "missing", math.NewInt(1))
	require.Error(t, err)
}
