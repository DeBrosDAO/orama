package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/relay/types"
)

func TestQuorumOneReporterPaysOnTheNextEpoch(t *testing.T) {
	f := newTestFixture(t)
	reporter := acc(1)
	operator := acc(2)
	f.init(t, 1, []sdk.AccAddress{reporter}, nil)
	rk := newRelayKey(t, "node-a", 0x11, operator, "10.1.2.3")
	f.register(t, rk, false)
	f.activate(t, []sdk.AccAddress{reporter}, []types.RelayObservation{obs(rk, 1, "1", false)})

	f.Emission.setCeiling(2, math.NewInt(1000))
	require.NoError(t, f.submit(t, reporter, 2, []types.RelayObservation{obs(rk, 40, "1", false)}))
	result, err := f.Keeper.SettleEpoch(f.Ctx, 2)
	require.NoError(t, err)
	require.True(t, result.Minted.Equal(math.NewInt(40)))
	require.True(t, f.Emission.mintedOf(2).Equal(math.NewInt(40)))
	require.True(t, f.Earnings.balance(operator).Equal(math.NewInt(36)))
	require.Equal(t, 1, f.Emission.calls)
}

func TestQuorumFailureMintsNothingAndDoesNotActivate(t *testing.T) {
	f := newTestFixture(t)
	a, b := acc(1), acc(2)
	operator := acc(3)
	f.init(t, 2, []sdk.AccAddress{a, b}, nil)
	rk := newRelayKey(t, "node-a", 0x11, operator, "10.1.2.3")
	f.register(t, rk, false)
	f.Emission.setCeiling(1, math.NewInt(1000))

	require.NoError(t, f.submit(t, a, 1, []types.RelayObservation{obs(rk, 40, "1", false)}))
	result, err := f.Keeper.SettleEpoch(f.Ctx, 1)
	require.NoError(t, err)
	require.False(t, result.QuorumMet)
	require.False(t, result.Activating)
	require.True(t, result.Minted.IsZero())
	require.Equal(t, 0, f.Emission.calls)
	activation, err := f.Keeper.Activation.Get(f.Ctx)
	require.NoError(t, err)
	require.False(t, activation.Active)
}

func TestPerRelayQuorumFailureMintsNothing(t *testing.T) {
	f := newTestFixture(t)
	a, b := acc(1), acc(2)
	op1, op2 := acc(3), acc(4)
	f.init(t, 2, []sdk.AccAddress{a, b}, nil)
	r1 := newRelayKey(t, "node-a", 0x11, op1, "10.1.2.3")
	r2 := newRelayKey(t, "node-b", 0x22, op2, "10.2.2.3")
	f.register(t, r1, false)
	f.register(t, r2, false)
	f.activate(t, []sdk.AccAddress{a, b}, []types.RelayObservation{obs(r1, 1, "1", false), obs(r2, 1, "1", false)})

	f.Emission.setCeiling(2, math.NewInt(1000))
	require.NoError(t, f.submit(t, a, 2, []types.RelayObservation{obs(r1, 50, "1", false)}))
	require.NoError(t, f.submit(t, b, 2, []types.RelayObservation{obs(r2, 50, "1", false)}))
	result, err := f.Keeper.SettleEpoch(f.Ctx, 2)
	require.NoError(t, err)
	require.True(t, result.QuorumMet)
	require.True(t, result.Minted.IsZero())
	require.True(t, f.Emission.mintedOf(2).IsZero())
	require.Empty(t, f.payouts(t, 2))
}

func TestMedianOfThreeIgnoresHundredXLiar(t *testing.T) {
	f := newTestFixture(t)
	reporters := []sdk.AccAddress{acc(1), acc(2), acc(3)}
	operator := acc(4)
	f.init(t, 3, reporters, nil)
	rk := newRelayKey(t, "node-a", 0x11, operator, "10.1.2.3")
	f.register(t, rk, false)
	f.activate(t, reporters, []types.RelayObservation{obs(rk, 1, "1", false)})

	f.Emission.setCeiling(2, math.NewInt(10_000))
	weights := []int64{10, 10, 1000}
	for i, reporter := range reporters {
		require.NoError(t, f.submit(t, reporter, 2, []types.RelayObservation{obs(rk, weights[i], "1", false)}))
	}
	result, err := f.Keeper.SettleEpoch(f.Ctx, 2)
	require.NoError(t, err)
	require.True(t, result.Minted.Equal(math.NewInt(10)), "median of 10, 10, 1000 is 10, got %s", result.Minted)
	require.True(t, f.Earnings.balance(operator).Equal(math.NewInt(10)))
	relay, err := f.Keeper.Relays.Get(f.Ctx, rk.fp)
	require.NoError(t, err)
	require.False(t, relay.Jailed)
}

func TestCaps(t *testing.T) {
	t.Run("per relay", func(t *testing.T) {
		f := newTestFixture(t)
		reporter, operator := acc(1), acc(2)
		f.init(t, 1, []sdk.AccAddress{reporter}, func(p *types.Params) {
			p.PerRelayCap = math.NewInt(100)
		})
		rk := newRelayKey(t, "node-a", 0x11, operator, "10.1.2.3")
		f.register(t, rk, false)
		f.activate(t, []sdk.AccAddress{reporter}, []types.RelayObservation{obs(rk, 1, "1", false)})
		f.Emission.setCeiling(2, math.NewInt(10_000))
		require.NoError(t, f.submit(t, reporter, 2, []types.RelayObservation{obs(rk, 250, "1", false)}))
		result, err := f.Keeper.SettleEpoch(f.Ctx, 2)
		require.NoError(t, err)
		require.True(t, result.Minted.Equal(math.NewInt(100)))
	})

	t.Run("per operator", func(t *testing.T) {
		f := newTestFixture(t)
		reporter, operator := acc(1), acc(2)
		f.init(t, 1, []sdk.AccAddress{reporter}, func(p *types.Params) {
			p.PerOperatorCap = math.NewInt(150)
		})
		r1 := newRelayKey(t, "node-a", 0x11, operator, "10.1.2.3")
		r2 := newRelayKey(t, "node-b", 0x22, operator, "10.2.2.3")
		f.register(t, r1, false)
		f.register(t, r2, false)
		f.activate(t, []sdk.AccAddress{reporter}, []types.RelayObservation{obs(r1, 1, "1", false), obs(r2, 1, "1", false)})
		f.Emission.setCeiling(2, math.NewInt(10_000))
		require.NoError(t, f.submit(t, reporter, 2, []types.RelayObservation{obs(r1, 100, "1", false), obs(r2, 100, "1", false)}))
		_, err := f.Keeper.SettleEpoch(f.Ctx, 2)
		require.NoError(t, err)
		got := f.payouts(t, 2)
		require.True(t, payoutFor(got, r1.fp).Equal(math.NewInt(75)))
		require.True(t, payoutFor(got, r2.fp).Equal(math.NewInt(75)))
		require.True(t, f.Earnings.balance(operator).Equal(math.NewInt(136)))
	})

	t.Run("per prefix 16", func(t *testing.T) {
		f := newTestFixture(t)
		reporter := acc(1)
		op1, op2 := acc(2), acc(3)
		f.init(t, 1, []sdk.AccAddress{reporter}, func(p *types.Params) {
			p.PerPrefix16Cap = math.NewInt(120)
		})
		r1 := newRelayKey(t, "node-a", 0x11, op1, "10.1.2.3")
		r2 := newRelayKey(t, "node-b", 0x22, op2, "10.1.9.9")
		f.register(t, r1, false)
		f.register(t, r2, false)
		f.activate(t, []sdk.AccAddress{reporter}, []types.RelayObservation{obs(r1, 1, "1", false), obs(r2, 1, "1", false)})
		f.Emission.setCeiling(2, math.NewInt(10_000))
		require.NoError(t, f.submit(t, reporter, 2, []types.RelayObservation{obs(r1, 100, "1", false), obs(r2, 100, "1", false)}))
		_, err := f.Keeper.SettleEpoch(f.Ctx, 2)
		require.NoError(t, err)
		got := f.payouts(t, 2)
		require.True(t, payoutFor(got, r1.fp).Equal(math.NewInt(60)))
		require.True(t, payoutFor(got, r2.fp).Equal(math.NewInt(60)))
	})
}

func TestUptimeBelowMinimumPaysNothing(t *testing.T) {
	f := newTestFixture(t)
	reporters := []sdk.AccAddress{acc(1), acc(2), acc(3)}
	operator := acc(4)
	f.init(t, 3, reporters, func(p *types.Params) {
		p.MinUptimeFraction = math.LegacyMustNewDecFromStr("0.8")
	})
	rk := newRelayKey(t, "node-a", 0x11, operator, "10.1.2.3")
	f.register(t, rk, false)
	f.activate(t, reporters, []types.RelayObservation{obs(rk, 1, "1", false)})

	f.Emission.setCeiling(2, math.NewInt(1000))
	uptimes := []string{"0.1", "0.1", "0.95"}
	for i, reporter := range reporters {
		require.NoError(t, f.submit(t, reporter, 2, []types.RelayObservation{obs(rk, 40, uptimes[i], false)}))
	}
	result, err := f.Keeper.SettleEpoch(f.Ctx, 2)
	require.NoError(t, err)
	require.True(t, result.Minted.IsZero())
	relay, err := f.Keeper.Relays.Get(f.Ctx, rk.fp)
	require.NoError(t, err)
	require.False(t, relay.Jailed)

	f2 := newTestFixture(t)
	reporter := acc(1)
	operator = acc(2)
	f2.init(t, 1, []sdk.AccAddress{reporter}, func(p *types.Params) {
		p.MinUptimeFraction = math.LegacyMustNewDecFromStr("0.8")
	})
	rk = newRelayKey(t, "node-b", 0x33, operator, "10.4.0.1")
	f2.register(t, rk, false)
	f2.activate(t, []sdk.AccAddress{reporter}, []types.RelayObservation{obs(rk, 1, "1", false)})
	f2.Emission.setCeiling(2, math.NewInt(1000))
	require.NoError(t, f2.submit(t, reporter, 2, []types.RelayObservation{obs(rk, 40, "0.8", false)}))
	result, err = f2.Keeper.SettleEpoch(f2.Ctx, 2)
	require.NoError(t, err)
	require.True(t, result.Minted.Equal(math.NewInt(40)))
}

func TestExitMultiplierOnlyWhenMedianExitFlagIsSet(t *testing.T) {
	f := newTestFixture(t)
	reporters := []sdk.AccAddress{acc(1), acc(2), acc(3)}
	opExit, opPlain := acc(4), acc(5)
	f.init(t, 3, reporters, func(p *types.Params) {
		p.ExitMultiplier = math.LegacyMustNewDecFromStr("2")
		p.PerRelayCap = math.NewInt(100)
	})
	exitRelay := newRelayKey(t, "exit", 0x11, opExit, "10.1.0.1")
	plain := newRelayKey(t, "plain", 0x22, opPlain, "10.2.0.1")
	f.register(t, exitRelay, true)
	f.register(t, plain, false)
	f.activate(t, reporters, []types.RelayObservation{obs(exitRelay, 1, "1", false), obs(plain, 1, "1", false)})

	f.Emission.setCeiling(2, math.NewInt(10_000))
	// Two of three set Exit on the exit relay (median set). One of three sets
	// it on the plain relay (median clear). The exit relay's weight is above
	// the per-relay cap, so the multiplier applies to the cap, not the raw weight.
	exitFlags := []bool{true, true, false}
	plainFlags := []bool{true, false, false}
	for i, reporter := range reporters {
		require.NoError(t, f.submit(t, reporter, 2, []types.RelayObservation{
			obs(exitRelay, 500, "1", exitFlags[i]),
			obs(plain, 30, "1", plainFlags[i]),
		}))
	}
	_, err := f.Keeper.SettleEpoch(f.Ctx, 2)
	require.NoError(t, err)
	got := f.payouts(t, 2)
	require.True(t, payoutFor(got, exitRelay.fp).Equal(math.NewInt(200)))
	require.True(t, payoutFor(got, plain.fp).Equal(math.NewInt(30)))
}

func TestPayProRataStopsAtCeiling(t *testing.T) {
	f := newTestFixture(t)
	reporter := acc(1)
	op1, op2 := acc(2), acc(3)
	f.init(t, 1, []sdk.AccAddress{reporter}, nil)
	r1 := newRelayKey(t, "node-a", 0x11, op1, "10.1.0.1")
	r2 := newRelayKey(t, "node-b", 0x22, op2, "10.2.0.1")
	f.register(t, r1, false)
	f.register(t, r2, false)
	f.activate(t, []sdk.AccAddress{reporter}, []types.RelayObservation{obs(r1, 1, "1", false), obs(r2, 1, "1", false)})

	f.Emission.setCeiling(2, math.NewInt(100))
	require.NoError(t, f.submit(t, reporter, 2, []types.RelayObservation{obs(r1, 300, "1", false), obs(r2, 100, "1", false)}))
	result, err := f.Keeper.SettleEpoch(f.Ctx, 2)
	require.NoError(t, err)
	require.True(t, result.Minted.Equal(math.NewInt(100)))
	require.True(t, result.Minted.LTE(result.Ceiling))
	got := f.payouts(t, 2)
	require.True(t, payoutFor(got, r1.fp).Equal(math.NewInt(75)))
	require.True(t, payoutFor(got, r2.fp).Equal(math.NewInt(25)))
	require.True(t, f.Emission.mintedOf(2).Equal(math.NewInt(100)))
	require.Equal(t, 1, f.Emission.calls)

	again, err := f.Keeper.SettleEpoch(f.Ctx, 2)
	require.NoError(t, err)
	require.True(t, again.Minted.Equal(result.Minted))
	require.Equal(t, 1, f.Emission.calls)

	checked, err := f.Keeper.CheckInvariants(f.Ctx)
	require.NoError(t, err)
	require.True(t, checked.CeilingHolds)
	require.True(t, checked.PayoutsMatch)

	// Under the ceiling, pay the owed amount and leave the rest unminted.
	f.Emission.setCeiling(3, math.NewInt(100))
	require.NoError(t, f.submit(t, reporter, 3, []types.RelayObservation{obs(r1, 40, "1", false)}))
	under, err := f.Keeper.SettleEpoch(f.Ctx, 3)
	require.NoError(t, err)
	require.True(t, under.Minted.Equal(math.NewInt(40)))
	require.True(t, under.Ceiling.Equal(math.NewInt(100)))
}

func TestCeilingInvariantBreaksWhenMintedIsInflated(t *testing.T) {
	f := newTestFixture(t)
	reporter, operator := acc(1), acc(2)
	f.init(t, 1, []sdk.AccAddress{reporter}, nil)
	rk := newRelayKey(t, "node-a", 0x11, operator, "10.1.0.1")
	f.register(t, rk, false)
	f.activate(t, []sdk.AccAddress{reporter}, []types.RelayObservation{obs(rk, 1, "1", false)})
	f.Emission.setCeiling(2, math.NewInt(50))
	require.NoError(t, f.submit(t, reporter, 2, []types.RelayObservation{obs(rk, 50, "1", false)}))
	_, err := f.Keeper.SettleEpoch(f.Ctx, 2)
	require.NoError(t, err)

	stored, err := f.Keeper.EpochResults.Get(f.Ctx, 2)
	require.NoError(t, err)
	stored.Minted = stored.Ceiling.AddRaw(1)
	require.NoError(t, f.Keeper.EpochResults.Set(f.Ctx, 2, stored))
	checked, err := f.Keeper.CheckInvariants(f.Ctx)
	require.NoError(t, err)
	require.False(t, checked.CeilingHolds)
}

func TestSettleEpochPaysNothingToARelayWhoseNodeLeft(t *testing.T) {
	f := newTestFixture(t)
	reporter := acc(1)
	stays, leaves := acc(2), acc(3)
	f.init(t, 1, []sdk.AccAddress{reporter}, nil)
	rkStays := newRelayKey(t, "node-stays", 0x11, stays, "10.1.2.3")
	rkLeaves := newRelayKey(t, "node-leaves", 0x22, leaves, "10.5.6.7")
	f.register(t, rkStays, false)
	f.register(t, rkLeaves, false)
	f.activate(t, []sdk.AccAddress{reporter}, []types.RelayObservation{obs(rkStays, 1, "1", false), obs(rkLeaves, 1, "1", false)})

	f.Emission.setCeiling(2, math.NewInt(1000))
	require.NoError(t, f.submit(t, reporter, 2, []types.RelayObservation{obs(rkStays, 40, "1", false), obs(rkLeaves, 60, "1", false)}))
	f.Nodes.leave("node-leaves")

	result, err := f.Keeper.SettleEpoch(f.Ctx, 2)

	require.NoError(t, err)
	require.True(t, result.Minted.Equal(math.NewInt(40)), "only the relay whose node is live is paid")
	require.True(t, f.Earnings.balance(stays).Equal(math.NewInt(36)))
	require.True(t, f.Earnings.balance(leaves).IsZero())
}

func TestSettleEpochPaysARelayAgainOnceItsNodeIsLiveAgain(t *testing.T) {
	f := newTestFixture(t)
	reporter := acc(1)
	operator := acc(2)
	f.init(t, 1, []sdk.AccAddress{reporter}, nil)
	rk := newRelayKey(t, "node-a", 0x11, operator, "10.1.2.3")
	f.register(t, rk, false)
	f.activate(t, []sdk.AccAddress{reporter}, []types.RelayObservation{obs(rk, 1, "1", false)})
	f.Nodes.leave("node-a")
	f.Emission.setCeiling(2, math.NewInt(1000))
	require.NoError(t, f.submit(t, reporter, 2, []types.RelayObservation{obs(rk, 40, "1", false)}))
	result, err := f.Keeper.SettleEpoch(f.Ctx, 2)
	require.NoError(t, err)
	require.True(t, result.Minted.IsZero(), "a node that left earns nothing for the epoch")

	f.Nodes.rejoin("node-a")
	f.Emission.setCeiling(3, math.NewInt(1000))
	require.NoError(t, f.submit(t, reporter, 3, []types.RelayObservation{obs(rk, 40, "1", false)}))
	result, err = f.Keeper.SettleEpoch(f.Ctx, 3)
	require.NoError(t, err)
	require.True(t, result.Minted.Equal(math.NewInt(40)), "a live node is paid again")
}
