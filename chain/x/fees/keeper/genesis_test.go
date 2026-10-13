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

func TestExportGenesis_feeBalancesRoundTrip(t *testing.T) {
	f := newTestFixture(t)
	hot := sdk.AccAddress("genesis_hot_key_addr_").String()
	f.initGenesis(t, func(gs *types.GenesisState) {
		gs.FeeBalances = []types.EarningsAccount{{Address: hot, Balance: math.NewInt(9)}}
	})
	f.Bank.fund(types.ModuleName, math.NewInt(9))

	exported, err := f.Keeper.ExportGenesis(f.Ctx)
	require.NoError(t, err)
	require.Equal(t, []types.EarningsAccount{{Address: hot, Balance: math.NewInt(9)}}, exported.FeeBalances)
	require.NoError(t, exported.Validate())
	inv, err := f.Keeper.CheckInvariants(f.Ctx)
	require.NoError(t, err)
	require.True(t, inv.EarningsMatchModule, inv.Detail)
}

func TestGenesisValidate_rejectsBadFeeBalances(t *testing.T) {
	addr := sdk.AccAddress("genesis_hot_key_addr_").String()
	cases := map[string][]types.EarningsAccount{
		"zero":      {{Address: addr, Balance: math.ZeroInt()}},
		"negative":  {{Address: addr, Balance: math.NewInt(-1)}},
		"bad addr":  {{Address: "nope", Balance: math.NewInt(1)}},
		"duplicate": {{Address: addr, Balance: math.NewInt(1)}, {Address: addr, Balance: math.NewInt(2)}},
	}
	for name, balances := range cases {
		t.Run(name, func(t *testing.T) {
			gs := types.DefaultGenesisState()
			gs.FeeBalances = balances
			require.Error(t, gs.Validate())
		})
	}
}
