package keeper_test

import (
	"testing"
	"time"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/emission/types"
	relaytypes "github.com/DeBrosOfficial/network/chain/x/relay/types"
)

func TestMintRelayReward_refusesASecondMintPastTheCeiling(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, func(gs *types.GenesisState) {
		gs.Params = types.NewParams(30*time.Second, 1, true)
	})
	ctx := f.Ctx.WithBlockTime(time.Unix(1_700_000_031, 0))
	require.NoError(t, f.Keeper.AdvanceBlock(ctx))

	ceiling, err := f.Keeper.RelayCeiling(ctx, 1)
	require.NoError(t, err)
	require.True(t, ceiling.Equal(math.NewInt(1_484_800_000_000)))

	before := f.Bank.GetSupply(ctx, params.BaseDenom).Amount
	require.NoError(t, f.Keeper.MintRelayReward(ctx, 1, ceiling))
	require.True(t, f.Bank.balanceOf(relaytypes.ModuleName).Equal(ceiling))

	err = f.Keeper.MintRelayReward(ctx, 1, math.NewInt(1))
	require.Error(t, err)
	require.True(t, f.Bank.balanceOf(relaytypes.ModuleName).Equal(ceiling))
	require.True(t, f.Bank.GetSupply(ctx, params.BaseDenom).Amount.Equal(before.Add(ceiling)))
}
