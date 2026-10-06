package keeper_test

import (
	"crypto/ed25519"
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/relay/types"
)

func TestRegisterRelay_unidentifiedNetworkUsesSharedBucket(t *testing.T) {
	f := newTestFixture(t)
	f.init(t, 1, []sdk.AccAddress{acc(1)}, nil)
	rk := newRelayKey(t, "node-a", 0x11, acc(2), "")
	f.register(t, rk, false)

	relay, err := f.Keeper.Relays.Get(f.Ctx, rk.fp)
	require.NoError(t, err)
	require.Equal(t, types.UnidentifiedPrefix16, relay.Prefix16)
}

func TestRegisterRelay_identifiedNetworkUsesItsPrefix(t *testing.T) {
	f := newTestFixture(t)
	f.init(t, 1, []sdk.AccAddress{acc(1)}, nil)
	rk := newRelayKey(t, "node-a", 0x11, acc(2), "203.0.113.0/16")
	f.register(t, rk, false)

	relay, err := f.Keeper.Relays.Get(f.Ctx, rk.fp)
	require.NoError(t, err)
	require.Equal(t, "203.0.0.0/16", relay.Prefix16)
}

func TestRegisterRelay_invalidNetworkRejected(t *testing.T) {
	f := newTestFixture(t)
	f.init(t, 1, []sdk.AccAddress{acc(1)}, nil)
	rk := newRelayKey(t, "node-a", 0x11, acc(2), "not-a-network")
	f.Nodes.add(rk)
	_, err := f.Msg.RegisterRelay(f.Ctx, &types.MsgRegisterRelay{
		Operator:         rk.operator.String(),
		NodeId:           rk.nodeID,
		RsaFingerprint:   rk.fp,
		Ed25519Signature: ed25519.Sign(rk.priv, types.CrossCertMessage(rk.nodeID, rk.fp)),
	})
	require.ErrorContains(t, err, "prefix /16")
}

func TestGenesisRoundTrip_unidentifiedBucket(t *testing.T) {
	f := newTestFixture(t)
	f.init(t, 1, []sdk.AccAddress{acc(1)}, nil)
	f.register(t, newRelayKey(t, "node-a", 0x11, acc(2), ""), false)

	exported, err := f.Keeper.ExportGenesis(f.Ctx)
	require.NoError(t, err)
	require.NoError(t, exported.Validate())
	require.Equal(t, types.UnidentifiedPrefix16, exported.Relays[0].Prefix16)

	again := newTestFixture(t)
	require.NoError(t, again.Keeper.InitGenesis(again.Ctx, *exported))
}

func TestGenesisRejectsBadPrefix16(t *testing.T) {
	f := newTestFixture(t)
	f.init(t, 1, []sdk.AccAddress{acc(1)}, nil)
	f.register(t, newRelayKey(t, "node-a", 0x11, acc(2), "10.9.0.0/16"), false)
	exported, err := f.Keeper.ExportGenesis(f.Ctx)
	require.NoError(t, err)

	for _, bad := range []string{"", "10.9.8.7", "nope"} {
		gs := *exported
		gs.Relays = append([]types.Relay(nil), exported.Relays...)
		gs.Relays[0].Prefix16 = bad
		require.Error(t, gs.Validate(), "prefix %q", bad)
	}
}

func TestSettle_unidentifiedRelaysShareOnePrefixCap(t *testing.T) {
	f := newTestFixture(t)
	reporter := acc(1)
	f.init(t, 1, []sdk.AccAddress{reporter}, func(p *types.Params) {
		p.PerPrefix16Cap = math.NewInt(120)
	})
	r1 := newRelayKey(t, "node-a", 0x11, acc(2), "")
	r2 := newRelayKey(t, "node-b", 0x22, acc(3), "")
	r3 := newRelayKey(t, "node-c", 0x33, acc(4), "10.7.0.0/16")
	f.register(t, r1, false)
	f.register(t, r2, false)
	f.register(t, r3, false)
	f.activate(t, []sdk.AccAddress{reporter}, []types.RelayObservation{obs(r1, 1, "1", false), obs(r2, 1, "1", false), obs(r3, 1, "1", false)})
	f.Emission.setCeiling(2, math.NewInt(10_000))
	require.NoError(t, f.submit(t, reporter, 2, []types.RelayObservation{obs(r1, 100, "1", false), obs(r2, 100, "1", false), obs(r3, 100, "1", false)}))
	_, err := f.Keeper.SettleEpoch(f.Ctx, 2)
	require.NoError(t, err)
	got := f.payouts(t, 2)
	require.True(t, payoutFor(got, r1.fp).Equal(math.NewInt(60)))
	require.True(t, payoutFor(got, r2.fp).Equal(math.NewInt(60)))
	require.True(t, payoutFor(got, r3.fp).Equal(math.NewInt(100)), "an identified /16 is capped on its own")
}
