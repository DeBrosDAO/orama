package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"

	"github.com/DeBrosOfficial/network/chain/x/fees/types"
)

func TestAdvanceBaseFee_risesWithFullBlocks(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, func(gs *types.GenesisState) {
		// A large enough base fee that a 12.5% integer-truncated move is actually visible;
		// DefaultParams' InitialBaseFee of 1 norama would round every change back to 1.
		gs.BaseFee = math.NewInt(1_000_000)
	})

	f.Ctx = f.Ctx.WithConsensusParams(cmtproto.ConsensusParams{Block: &cmtproto.BlockParams{MaxGas: 100}})
	f.Ctx = f.Ctx.WithBlockGasMeter(storetypes.NewGasMeter(100))
	f.Ctx.BlockGasMeter().ConsumeGas(100, "test full block")

	before, err := f.Keeper.BaseFee.Get(f.Ctx)
	require.NoError(t, err)

	require.NoError(t, f.Keeper.AdvanceBaseFee(f.Ctx))

	after, err := f.Keeper.BaseFee.Get(f.Ctx)
	require.NoError(t, err)
	require.True(t, after.GT(before), "base fee should rise after a full block: before=%s after=%s", before, after)
}

func TestAdvanceBaseFee_noOpWithoutABlockGasLimit(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	f.Ctx = f.Ctx.WithConsensusParams(cmtproto.ConsensusParams{Block: &cmtproto.BlockParams{MaxGas: -1}})
	f.Ctx = f.Ctx.WithBlockGasMeter(storetypes.NewGasMeter(1))

	before, err := f.Keeper.BaseFee.Get(f.Ctx)
	require.NoError(t, err)
	require.NoError(t, f.Keeper.AdvanceBaseFee(f.Ctx))
	after, err := f.Keeper.BaseFee.Get(f.Ctx)
	require.NoError(t, err)
	require.True(t, after.Equal(before))
}
