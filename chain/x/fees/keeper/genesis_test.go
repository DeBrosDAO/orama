package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/fees/types"
)

func TestInitGenesis_defaultState(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)

	baseFee, err := f.Keeper.BaseFee.Get(f.Ctx)
	require.NoError(t, err)
	require.True(t, baseFee.Equal(types.DefaultParams().InitialBaseFee))
}

func TestExportGenesis_roundTrip(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, func(gs *types.GenesisState) {
		gs.BaseFee = math.NewInt(42)
		gs.EarningsAccounts = []types.EarningsAccount{{Address: sdk.AccAddress("genesis_earnings_addr").String(), Balance: math.NewInt(7)}}
	})

	exported, err := f.Keeper.ExportGenesis(f.Ctx)
	require.NoError(t, err)
	require.True(t, exported.BaseFee.Equal(math.NewInt(42)))
	require.Len(t, exported.EarningsAccounts, 1)
	require.NoError(t, exported.Validate())
}
