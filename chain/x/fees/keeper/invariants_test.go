package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/fees/types"
)

func TestCheckInvariants_holdAfterFeeSettlement(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)

	payer := sdk.AccAddress("fee_payer_account____")
	proposer := sdk.AccAddress("block_proposer_acct__")
	f.Bank.fund(payer.String(), math.NewInt(1_000))
	require.NoError(t, f.Keeper.SettleFee(f.Ctx, payer, proposer, math.NewInt(600), math.NewInt(400), true))

	got, err := f.Keeper.CheckInvariants(f.Ctx)
	require.NoError(t, err)
	require.True(t, got.EarningsMatchModule, got.Detail)
	require.True(t, got.DepositsMatchModule, got.Detail)
	require.True(t, got.FeesBalance, got.Detail)
	require.True(t, got.Collected.Equal(math.NewInt(1_000)))
	require.True(t, got.Burned.Equal(math.NewInt(600)))
	require.True(t, got.Distributed.Equal(math.NewInt(400)))

	tip, err := f.Keeper.GetEarnings(f.Ctx, proposer)
	require.NoError(t, err)
	require.True(t, tip.Equal(math.NewInt(400)), "the tip must sit in the proposer's earnings")
	require.True(t, f.Bank.burned.Equal(math.NewInt(600)))
}

func TestCheckInvariants_breakWhenTheLedgerDrifts(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	addr := sdk.AccAddress("drifted_earnings_____").String()
	require.NoError(t, f.Keeper.Earnings.Set(f.Ctx, addr, math.NewInt(5)))

	got, err := f.Keeper.CheckInvariants(f.Ctx)
	require.NoError(t, err)
	require.False(t, got.EarningsMatchModule, got.Detail)
}

func TestExportGenesis_feeCountersRoundTrip(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, func(gs *types.GenesisState) {
		gs.CumulativeCollected = math.NewInt(10)
		gs.CumulativeBurned = math.NewInt(7)
		gs.CumulativeDistributed = math.NewInt(3)
	})
	exported, err := f.Keeper.ExportGenesis(f.Ctx)
	require.NoError(t, err)
	require.True(t, exported.CumulativeCollected.Equal(math.NewInt(10)))
	require.True(t, exported.CumulativeBurned.Equal(math.NewInt(7)))
	require.True(t, exported.CumulativeDistributed.Equal(math.NewInt(3)))
	require.NoError(t, exported.Validate())
	_ = params.BaseDenom
}
