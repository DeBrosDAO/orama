package keeper_test

import (
	"testing"
	"time"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/emission/types"
)

func TestMintDevelopmentSpend_mintsOnlyRemainingCeiling(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, func(gs *types.GenesisState) {
		gs.Params = types.NewParams(30*time.Second, 1, true)
	})
	ctx := f.Ctx.WithBlockTime(time.Unix(1_700_000_031, 0))
	require.NoError(t, f.Keeper.AdvanceBlock(ctx))

	record, err := f.Keeper.Ceilings.Get(ctx, 1)
	require.NoError(t, err)
	ceiling := record.DevelopmentCeiling
	require.True(t, ceiling.Equal(math.NewInt(742_400_000_000)))
	validatorBefore := f.Bank.GetSupply(ctx, params.BaseDenom).Amount

	half := ceiling.QuoRaw(2)
	moduleBefore := f.Bank.balanceOf(types.ModuleName)
	module, err := f.Keeper.MintDevelopmentSpend(ctx, 1, half)
	require.NoError(t, err)
	require.Equal(t, types.ModuleName, module)
	require.True(t, f.Bank.balanceOf(types.ModuleName).Equal(moduleBefore.Add(half)))

	// The other half of the ceiling, and only that, is still mintable.
	_, err = f.Keeper.MintDevelopmentSpend(ctx, 1, half.AddRaw(1))
	require.Error(t, err)
	require.True(t, f.Bank.balanceOf(types.ModuleName).Equal(moduleBefore.Add(half)), "a refused amount must not mint")

	module, err = f.Keeper.MintDevelopmentSpend(ctx, 1, half)
	require.NoError(t, err)
	require.Equal(t, types.ModuleName, module)
	require.True(t, f.Bank.balanceOf(types.ModuleName).Equal(moduleBefore.Add(ceiling)))

	_, err = f.Keeper.MintDevelopmentSpend(ctx, 1, math.NewInt(1))
	require.Error(t, err)
	require.True(t, f.Bank.balanceOf(types.ModuleName).Equal(moduleBefore.Add(ceiling)))

	state, err := f.Keeper.EpochState.Get(ctx)
	require.NoError(t, err)
	require.True(t, state.CumulativeMinted.Equal(validatorBefore), "development mints must not count as validator mints")
	require.True(t, state.CumulativeDevelopmentMinted.Equal(ceiling))
	require.True(t, f.Bank.GetSupply(ctx, params.BaseDenom).Amount.Equal(validatorBefore.Add(ceiling)))

	_, broken := f.Keeper.CheckSupplyInvariant(ctx)
	require.False(t, broken)

	// Storage and relay ceilings are not a mint path. Asking for the storage
	// amount is just another amount above the development remainder.
	_, err = f.Keeper.MintDevelopmentSpend(ctx, 1, record.StorageCeiling)
	require.Error(t, err)
}

func TestMintDevelopmentSpend_refusesAnythingButTheDevelopmentRemainder(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, func(gs *types.GenesisState) {
		gs.Params = types.NewParams(30*time.Second, 1, true)
	})
	ctx := f.Ctx.WithBlockTime(time.Unix(1_700_000_031, 0))
	require.NoError(t, f.Keeper.AdvanceBlock(ctx))

	supplyBefore := f.Bank.GetSupply(ctx, params.BaseDenom).Amount
	cases := []math.Int{
		math.Int{},
		math.ZeroInt(),
		math.NewInt(-1),
	}
	for _, amount := range cases {
		_, err := f.Keeper.MintDevelopmentSpend(ctx, 1, amount)
		require.Error(t, err, amount.String())
	}
	_, err := f.Keeper.MintDevelopmentSpend(ctx, 2, math.NewInt(1))
	require.Error(t, err)
	_, err = f.Keeper.MintDevelopmentSpend(ctx, 0, math.NewInt(1))
	require.Error(t, err)

	require.True(t, f.Bank.GetSupply(ctx, params.BaseDenom).Amount.Equal(supplyBefore))
	state, err := f.Keeper.EpochState.Get(ctx)
	require.NoError(t, err)
	require.True(t, nonNilDev(state.CumulativeDevelopmentMinted).IsZero())
}

func nonNilDev(v math.Int) math.Int {
	if v.IsNil() {
		return math.ZeroInt()
	}
	return v
}
