package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/relay/types"
)

func TestInitGenesisDefaultAndReporterRoundTrip(t *testing.T) {
	f := newTestFixture(t)
	require.NoError(t, types.DefaultGenesisState().Validate())
	reporter := acc(1)
	f.init(t, 1, []sdk.AccAddress{reporter}, nil)
	operator := acc(2)
	rk := newRelayKey(t, "node-a", 0x11, operator, "10.9.8.7")
	f.register(t, rk, true)

	exported, err := f.Keeper.ExportGenesis(f.Ctx)
	require.NoError(t, err)
	require.NoError(t, exported.Validate())
	require.Equal(t, []string{reporter.String()}, exported.Reporters)
	require.Len(t, exported.Relays, 1)
	require.Equal(t, "10.9.0.0/16", exported.Relays[0].Prefix16)
	require.True(t, exported.Relays[0].Exit)
	require.False(t, exported.Relays[0].Jailed)

	again := newTestFixture(t)
	require.NoError(t, again.Keeper.InitGenesis(again.Ctx, *exported))
	got, err := again.Keeper.ExportGenesis(again.Ctx)
	require.NoError(t, err)
	require.Equal(t, exported.Reporters, got.Reporters)
	require.Equal(t, exported.Relays[0].NodeId, got.Relays[0].NodeId)
	require.Equal(t, exported.Relays[0].Operator, got.Relays[0].Operator)
}

func TestGenesisRejectsDuplicateReporters(t *testing.T) {
	gs := types.DefaultGenesisState()
	addr := acc(1).String()
	gs.Reporters = []string{addr, addr}
	require.Error(t, gs.Validate())

	gs = types.DefaultGenesisState()
	gs.Params.MinReportersQuorum = 0
	require.Error(t, gs.Validate())
}
