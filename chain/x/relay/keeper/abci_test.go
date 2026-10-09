package keeper_test

import (
	"crypto/ed25519"
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/relay/keeper"
	"github.com/DeBrosOfficial/network/chain/x/relay/types"
)

func hasEpochResult(t *testing.T, f *testFixture, epoch uint64) bool {
	t.Helper()
	has, err := f.Keeper.EpochResults.Has(f.Ctx, epoch)
	require.NoError(t, err)
	return has
}

func (f *testFixture) reportsOf(t *testing.T, epoch uint64) []types.CompleteReport {
	t.Helper()
	exported, err := f.Keeper.ExportGenesis(f.Ctx)
	require.NoError(t, err)
	var out []types.CompleteReport
	for _, report := range exported.Reports {
		if report.Epoch == epoch {
			out = append(out, report)
		}
	}
	return out
}

func registerMsg(rk relayKey) *types.MsgRegisterRelay {
	return &types.MsgRegisterRelay{
		Operator:         rk.operator.String(),
		NodeId:           rk.nodeID,
		RsaFingerprint:   rk.fp,
		Ed25519Signature: ed25519.Sign(rk.priv, types.CrossCertMessage(rk.nodeID, rk.fp)),
	}
}

func TestEndBlock_settlesAnEpochOnlyAfterItsReportWindow(t *testing.T) {
	f := newTestFixture(t)
	reporter, operator := acc(1), acc(2)
	f.init(t, 1, []sdk.AccAddress{reporter}, nil)
	rk := newRelayKey(t, "node-a", 0x11, operator, "10.1.2.3")
	f.register(t, rk, false)

	// Epoch 1 closed and is reported on while the chain is in epoch 2.
	require.NoError(t, f.submit(t, reporter, 1, []types.RelayObservation{obs(rk, 1, "1", false)}))
	require.NoError(t, f.Keeper.EndBlock(f.Ctx))
	require.False(t, hasEpochResult(t, f, 1), "an epoch inside its report window must not settle")

	f.Emission.current = 1 + types.ReportWindowEpochs + 1
	require.NoError(t, f.Keeper.EndBlock(f.Ctx))
	result, err := f.Keeper.EpochResults.Get(f.Ctx, 1)
	require.NoError(t, err)
	require.True(t, result.Activating, "the first epoch with quorum activates rewards")

	// The next epoch pays, again with no message calling settlement.
	f.Emission.setCeiling(2, math.NewInt(1000))
	require.NoError(t, f.submit(t, reporter, 2, []types.RelayObservation{obs(rk, 40, "1", false)}))
	f.Emission.current = 2 + types.ReportWindowEpochs + 1
	require.NoError(t, f.Keeper.EndBlock(f.Ctx))
	require.True(t, f.Earnings.balance(operator).Equal(math.NewInt(36)))
	require.True(t, f.Emission.mintedOf(2).Equal(math.NewInt(40)))
	require.Empty(t, f.reportsOf(t, 2), "settlement deletes the epoch's reports")
}

func TestSettle_paysThroughTheServiceSplitAndTheOperatorGetsTheRemainder(t *testing.T) {
	f := newTestFixture(t)
	reporter := acc(1)
	opA, opB, opC := acc(2), acc(3), acc(4)
	f.init(t, 1, []sdk.AccAddress{reporter}, nil)
	a := newRelayKey(t, "node-a", 0x11, opA, "10.1.0.1")
	b := newRelayKey(t, "node-b", 0x22, opB, "10.2.0.1")
	c := newRelayKey(t, "node-c", 0x33, opC, "10.3.0.1")
	for _, rk := range []relayKey{a, b, c} {
		f.register(t, rk, false)
	}
	f.activate(t, []sdk.AccAddress{reporter}, []types.RelayObservation{obs(a, 1, "1", false)})

	f.Emission.setCeiling(2, math.NewInt(100_000))
	// 1001 splits 50 burned, 50 archive, 901 to the operator (the 1 left by rounding);
	// 19 is below the first whole unit of either 5% share, so the operator keeps all of it;
	// 1999 splits 99 / 99 / 1801.
	require.NoError(t, f.submit(t, reporter, 2, []types.RelayObservation{obs(a, 1001, "1", false), obs(b, 19, "1", false), obs(c, 1999, "1", false)}))
	result, err := f.Keeper.SettleEpoch(f.Ctx, 2)
	require.NoError(t, err)
	require.True(t, result.Minted.Equal(math.NewInt(1001+19+1999)))

	require.True(t, f.Earnings.balance(opA).Equal(math.NewInt(901)))
	require.True(t, f.Earnings.balance(opB).Equal(math.NewInt(19)))
	require.True(t, f.Earnings.balance(opC).Equal(math.NewInt(1801)))
	require.True(t, f.Service.burned.Equal(math.NewInt(50+0+99)))
	require.True(t, f.Service.archive.Equal(math.NewInt(50+0+99)))
	total := f.Earnings.balance(opA).Add(f.Earnings.balance(opB)).Add(f.Earnings.balance(opC)).Add(f.Service.burned).Add(f.Service.archive)
	require.True(t, total.Equal(result.Minted), "everything minted is paid, burned or in the archive fund")
	require.True(t, payoutFor(f.payouts(t, 2), a.fp).Equal(math.NewInt(1001)), "payout rows stay gross")
}

func TestEndBlock_anIdleChainWritesNothing(t *testing.T) {
	f := newTestFixture(t)
	f.init(t, 1, []sdk.AccAddress{acc(1)}, nil)
	f.Emission.current = 50
	require.NoError(t, f.Keeper.EndBlock(f.Ctx))
	results, err := f.Keeper.ExportGenesis(f.Ctx)
	require.NoError(t, err)
	require.Empty(t, results.EpochResults)
	require.Empty(t, results.Payouts)
}

func TestEndBlock_settlesOneEpochPerBlockOldestFirst(t *testing.T) {
	f := newTestFixture(t)
	reporter, operator := acc(1), acc(2)
	f.init(t, 1, []sdk.AccAddress{reporter}, nil)
	rk := newRelayKey(t, "node-a", 0x11, operator, "10.1.2.3")
	f.register(t, rk, false)
	require.NoError(t, f.submit(t, reporter, 3, []types.RelayObservation{obs(rk, 1, "1", false)}))
	require.NoError(t, f.submit(t, reporter, 4, []types.RelayObservation{obs(rk, 1, "1", false)}))

	f.Emission.setCeiling(4, math.NewInt(1000))
	f.Emission.current = 10
	require.NoError(t, f.Keeper.EndBlock(f.Ctx))
	require.True(t, hasEpochResult(t, f, 3))
	require.False(t, hasEpochResult(t, f, 4))
	require.NoError(t, f.Keeper.EndBlock(f.Ctx))
	require.True(t, hasEpochResult(t, f, 4))
}

func TestEndBlock_dropsAnUnfinishedReportAtSettlement(t *testing.T) {
	f := newTestFixture(t)
	reporter, operator := acc(1), acc(2)
	f.init(t, 1, []sdk.AccAddress{reporter}, nil)
	rk := newRelayKey(t, "node-a", 0x11, operator, "10.1.2.3")
	f.register(t, rk, false)
	entry := obs(rk, 10, "1", false)
	root, err := types.InputsRoot([]types.RelayObservation{entry})
	require.NoError(t, err)
	f.closeEpoch(5)
	_, err = f.Msg.ReportEpoch(f.Ctx, &types.MsgReportEpoch{
		Reporter: reporter.String(), Epoch: 5, ChunkIndex: 0, ChunkCount: 2,
		Entries: []types.RelayObservation{entry}, InputsRoot: root,
	})
	require.NoError(t, err)

	f.Emission.current = 5 + types.ReportWindowEpochs + 1
	require.NoError(t, f.Keeper.EndBlock(f.Ctx))
	result, err := f.Keeper.EpochResults.Get(f.Ctx, 5)
	require.NoError(t, err)
	require.False(t, result.QuorumMet)
	exported, err := f.Keeper.ExportGenesis(f.Ctx)
	require.NoError(t, err)
	require.Empty(t, exported.Chunks, "an unfinished report must not stay in state forever")
}

func TestEndBlock_anOlderUnfinishedReportSettlesBeforeANewerCompleteOne(t *testing.T) {
	f := newTestFixture(t)
	reporter, operator := acc(1), acc(2)
	f.init(t, 1, []sdk.AccAddress{reporter}, nil)
	rk := newRelayKey(t, "node-a", 0x11, operator, "10.1.2.3")
	f.register(t, rk, false)
	entries := []types.RelayObservation{obs(rk, 1, "1", false)}
	root, err := types.InputsRoot(entries)
	require.NoError(t, err)

	f.closeEpoch(2)
	_, err = f.Msg.ReportEpoch(f.Ctx, &types.MsgReportEpoch{
		Reporter: reporter.String(), Epoch: 2, ChunkIndex: 0, ChunkCount: 2, Entries: entries, InputsRoot: root,
	})
	require.NoError(t, err)
	require.NoError(t, f.submit(t, reporter, 3, entries))

	f.Emission.current = 20
	require.NoError(t, f.Keeper.EndBlock(f.Ctx))
	require.True(t, hasEpochResult(t, f, 2))
	require.False(t, hasEpochResult(t, f, 3))
}

func TestReportEpoch_onlyForAClosedEpochInsideItsWindow(t *testing.T) {
	f := newTestFixture(t)
	reporter, operator := acc(1), acc(2)
	f.init(t, 1, []sdk.AccAddress{reporter}, nil)
	rk := newRelayKey(t, "node-a", 0x11, operator, "10.1.2.3")
	f.register(t, rk, false)
	entries := []types.RelayObservation{obs(rk, 1, "1", false)}

	f.closeEpoch(4)
	for _, epoch := range []uint64{3, 5, 9} {
		f.Emission.setCeiling(epoch, math.NewInt(1000)) // refusals below are about the window, not the ceiling
	}
	require.ErrorIs(t, f.submitAt(t, reporter, 5, entries), types.ErrReportWindow, "the epoch in progress")
	require.ErrorIs(t, f.submitAt(t, reporter, 9, entries), types.ErrReportWindow, "a future epoch")
	require.NoError(t, f.submitAt(t, reporter, 4, entries))
	require.ErrorIs(t, f.submitAt(t, reporter, 3, entries), types.ErrReportWindow, "a report after the window")
}

func TestReportEpoch_refusesAnEpochWithoutACeiling(t *testing.T) {
	f := newTestFixture(t)
	reporter, operator := acc(1), acc(2)
	f.init(t, 1, []sdk.AccAddress{reporter}, nil)
	rk := newRelayKey(t, "node-a", 0x11, operator, "10.1.2.3")
	f.register(t, rk, false)

	f.Emission.current = 5 // epoch 4 closed, but x/emission recorded no ceiling for it
	err := f.submitAt(t, reporter, 4, []types.RelayObservation{obs(rk, 1, "1", false)})
	require.ErrorIs(t, err, types.ErrReportWindow)
	require.Contains(t, err.Error(), "no relay ceiling")
}

func TestObservation_weightIsBoundedAndTheMedianCannotOverflow(t *testing.T) {
	f := newTestFixture(t)
	a, b, operator := acc(1), acc(2), acc(3)
	f.init(t, 2, []sdk.AccAddress{a, b}, nil)
	rk := newRelayKey(t, "node-a", 0x11, operator, "10.1.2.3")
	f.register(t, rk, false)

	huge := obs(rk, 1, "1", false)
	huge.ConsensusWeight = math.NewIntFromUint64(1 << 62).AddRaw(1)
	require.Error(t, huge.Validate(), "a weight above the bound is refused at the boundary")
	require.Error(t, f.submit(t, a, 1, []types.RelayObservation{huge}))

	// Two reporters at the bound: the average of the two middle values must not overflow.
	atBound := obs(rk, 1, "1", false)
	atBound.ConsensusWeight = types.MaxConsensusWeight
	f.activate(t, []sdk.AccAddress{a, b}, []types.RelayObservation{atBound})
	f.Emission.setCeiling(2, math.NewInt(1000))
	require.NoError(t, f.submit(t, a, 2, []types.RelayObservation{atBound}))
	require.NoError(t, f.submit(t, b, 2, []types.RelayObservation{atBound}))
	result, err := f.Keeper.SettleEpoch(f.Ctx, 2)
	require.NoError(t, err)
	require.True(t, result.Minted.LTE(result.Ceiling))
}

func TestEndBlock_dropsReportsLeftForASettledEpoch(t *testing.T) {
	f := newTestFixture(t)
	reporter, operator := acc(1), acc(2)
	f.init(t, 1, []sdk.AccAddress{reporter}, nil)
	rk := newRelayKey(t, "node-a", 0x11, operator, "10.1.2.3")
	f.register(t, rk, false)
	entries := []types.RelayObservation{obs(rk, 1, "1", false)}
	require.NoError(t, f.submit(t, reporter, 1, entries))
	_, err := f.Keeper.SettleEpoch(f.Ctx, 1)
	require.NoError(t, err)

	// An imported genesis can carry a report for an epoch that already has a result.
	root, err := types.InputsRoot(entries)
	require.NoError(t, err)
	require.NoError(t, f.Keeper.Reports.Set(f.Ctx, collections.Join(uint64(1), reporter.String()), types.CompleteReport{
		Epoch: 1, Reporter: reporter.String(), InputsRoot: root, Entries: entries,
	}))
	require.NoError(t, f.submit(t, reporter, 3, entries))

	f.Emission.current = 20
	require.NoError(t, f.Keeper.EndBlock(f.Ctx))
	require.Empty(t, f.reportsOf(t, 1), "the leftover report is dropped")
	require.NoError(t, f.Keeper.EndBlock(f.Ctx))
	require.True(t, hasEpochResult(t, f, 3), "the epoch behind it is no longer held back")
}

func TestRegisterRelay_refusesAReporterAsOperator(t *testing.T) {
	f := newTestFixture(t)
	reporter := acc(1)
	f.init(t, 1, []sdk.AccAddress{reporter}, nil)
	rk := newRelayKey(t, "node-a", 0x11, reporter, "10.1.2.3")
	f.Nodes.add(rk)
	_, err := f.Msg.RegisterRelay(f.Ctx, registerMsg(rk))
	require.ErrorIs(t, err, types.ErrReporterOperatesRelay)
}

func TestUpdateReporters_refusesARelayOperator(t *testing.T) {
	f := newTestFixture(t)
	reporter, operator := acc(1), acc(2)
	f.init(t, 1, []sdk.AccAddress{reporter}, nil)
	f.register(t, newRelayKey(t, "node-a", 0x11, operator, "10.1.2.3"), false)

	ctx := keeper.WithAllowReporterChange(f.Ctx)
	_, err := f.Msg.UpdateReporters(ctx, &types.MsgUpdateReporters{Signer: reporter.String(), Reporters: []string{reporter.String(), operator.String()}})
	require.ErrorIs(t, err, types.ErrReporterOperatesRelay)
	set, err := f.Keeper.ExportGenesis(f.Ctx)
	require.NoError(t, err)
	require.Equal(t, []string{reporter.String()}, set.Reporters, "a refused change leaves the set as it was")
}

func TestGenesis_rejectsAReporterThatOperatesARelay(t *testing.T) {
	f := newTestFixture(t)
	reporter, operator := acc(1), acc(2)
	f.init(t, 1, []sdk.AccAddress{reporter}, nil)
	f.register(t, newRelayKey(t, "node-a", 0x11, operator, "10.1.2.3"), false)
	exported, err := f.Keeper.ExportGenesis(f.Ctx)
	require.NoError(t, err)
	require.NoError(t, exported.Validate())

	exported.Reporters = append(exported.Reporters, operator.String())
	require.ErrorIs(t, exported.Validate(), types.ErrReporterOperatesRelay)
}
