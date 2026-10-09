package keeper_test

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/relay/types"
)

func TestNonReporterIsRefused(t *testing.T) {
	f := newTestFixture(t)
	reporter, stranger := acc(1), acc(9)
	operator := acc(2)
	f.init(t, 1, []sdk.AccAddress{reporter}, nil)
	rk := newRelayKey(t, "node-a", 0x11, operator, "10.1.0.1")
	f.register(t, rk, false)

	err := f.submit(t, stranger, 1, []types.RelayObservation{obs(rk, 10, "1", false)})
	require.ErrorIs(t, err, types.ErrNotReporter)
	has, err := f.Keeper.Reports.Has(f.Ctx, collections.Join(uint64(1), stranger.String()))
	require.NoError(t, err)
	require.False(t, has)
}

func TestChunkedReportsReassembleInIndexOrder(t *testing.T) {
	f := newTestFixture(t)
	reporter := acc(1)
	op1, op2 := acc(2), acc(3)
	f.init(t, 1, []sdk.AccAddress{reporter}, nil)
	r1 := newRelayKey(t, "node-a", 0x11, op1, "10.1.0.1")
	r2 := newRelayKey(t, "node-b", 0x22, op2, "10.2.0.1")
	f.register(t, r1, false)
	f.register(t, r2, false)

	entries := []types.RelayObservation{obs(r1, 10, "1", false), obs(r2, 20, "1", false)}
	root, err := types.InputsRoot(entries)
	require.NoError(t, err)

	f.closeEpoch(1)
	// Chunk 1 arrives before chunk 0. Reassembly must follow chunk_index,
	// because inputs_root is over that order.
	_, err = f.Msg.ReportEpoch(f.Ctx, &types.MsgReportEpoch{
		Reporter:   reporter.String(),
		Epoch:      1,
		ChunkIndex: 1,
		ChunkCount: 2,
		Entries:    []types.RelayObservation{entries[1]},
		InputsRoot: root,
	})
	require.NoError(t, err)
	res, err := f.Msg.ReportEpoch(f.Ctx, &types.MsgReportEpoch{
		Reporter:   reporter.String(),
		Epoch:      1,
		ChunkIndex: 0,
		ChunkCount: 2,
		Entries:    []types.RelayObservation{entries[0]},
		InputsRoot: root,
	})
	require.NoError(t, err)
	require.True(t, res.Complete)

	stored, err := f.Keeper.Reports.Get(f.Ctx, collections.Join(uint64(1), reporter.String()))
	require.NoError(t, err)
	require.Len(t, stored.Entries, 2)
	require.True(t, bytes.Equal(stored.Entries[0].RsaFingerprint, r1.fp))
	require.True(t, bytes.Equal(stored.Entries[1].RsaFingerprint, r2.fp))
	require.True(t, bytes.Equal(stored.InputsRoot, root))

	// The chunks themselves are dropped once the report is complete.
	var chunks int
	err = f.Keeper.Chunks.Walk(f.Ctx, nil, func(_ collections.Triple[uint64, string, uint32], _ types.ReportChunk) (bool, error) {
		chunks++
		return false, nil
	})
	require.NoError(t, err)
	require.Zero(t, chunks)

	// Identical resubmission of a finished report is a no-op.
	again, err := f.Msg.ReportEpoch(f.Ctx, &types.MsgReportEpoch{
		Reporter:   reporter.String(),
		Epoch:      1,
		ChunkIndex: 0,
		ChunkCount: 2,
		Entries:    []types.RelayObservation{entries[0]},
		InputsRoot: root,
	})
	require.NoError(t, err)
	require.True(t, again.Complete)
}

func TestInputsRootRecomputesAndRejectsAMutatedEntry(t *testing.T) {
	f := newTestFixture(t)
	reporter, operator := acc(1), acc(2)
	f.init(t, 1, []sdk.AccAddress{reporter}, nil)
	rk := newRelayKey(t, "node-a", 0x11, operator, "10.1.0.1")
	f.register(t, rk, false)

	entries := []types.RelayObservation{obs(rk, 10, "1", false)}
	root, err := types.InputsRoot(entries)
	require.NoError(t, err)
	recomputed, err := types.InputsRoot(entries)
	require.NoError(t, err)
	require.True(t, bytes.Equal(root, recomputed))

	mutated := obs(rk, 11, "1", false)
	bad, err := types.InputsRoot([]types.RelayObservation{mutated})
	require.NoError(t, err)
	require.False(t, bytes.Equal(root, bad))

	f.closeEpoch(1)
	_, err = f.Msg.ReportEpoch(f.Ctx, &types.MsgReportEpoch{
		Reporter:   reporter.String(),
		Epoch:      1,
		ChunkIndex: 0,
		ChunkCount: 1,
		Entries:    []types.RelayObservation{mutated},
		InputsRoot: root,
	})
	require.ErrorIs(t, err, types.ErrInputsRootMismatch)
	has, err := f.Keeper.Reports.Has(f.Ctx, collections.Join(uint64(1), reporter.String()))
	require.NoError(t, err)
	require.False(t, has)

	require.NoError(t, f.submit(t, reporter, 1, entries))
	stored, err := f.Keeper.Reports.Get(f.Ctx, collections.Join(uint64(1), reporter.String()))
	require.NoError(t, err)
	require.True(t, bytes.Equal(stored.InputsRoot, root))
}

func TestRSAFingerprintMustMatchEd25519CrossSignature(t *testing.T) {
	f := newTestFixture(t)
	operator := acc(2)
	f.init(t, 1, []sdk.AccAddress{acc(1)}, nil)
	rk := newRelayKey(t, "node-a", 0x11, operator, "10.1.0.1")
	f.Nodes.add(rk)

	_, err := f.Msg.RegisterRelay(f.Ctx, &types.MsgRegisterRelay{
		Operator:         operator.String(),
		NodeId:           rk.nodeID,
		RsaFingerprint:   rk.fp,
		Ed25519Signature: bytes.Repeat([]byte{0xab}, types.Ed25519SigLen),
	})
	require.ErrorIs(t, err, types.ErrCrossCertMismatch)

	otherPub, otherPriv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	_ = otherPub
	_, err = f.Msg.RegisterRelay(f.Ctx, &types.MsgRegisterRelay{
		Operator:         operator.String(),
		NodeId:           rk.nodeID,
		RsaFingerprint:   rk.fp,
		Ed25519Signature: ed25519.Sign(otherPriv, types.CrossCertMessage(rk.nodeID, rk.fp)),
	})
	require.ErrorIs(t, err, types.ErrCrossCertMismatch)

	f.register(t, rk, false)

	bad := obs(rk, 10, "1", false)
	bad.Ed25519Id = bytes.Repeat([]byte{0x07}, types.Ed25519PubLen)
	err = f.submit(t, acc(1), 1, []types.RelayObservation{bad})
	require.ErrorIs(t, err, types.ErrEd25519Mismatch)
	require.True(t, errors.Is(err, types.ErrEd25519Mismatch))
}

func TestRepeatedChunkIsIdempotentUntilComplete(t *testing.T) {
	f := newTestFixture(t)
	reporter, operator := acc(1), acc(2)
	f.init(t, 1, []sdk.AccAddress{reporter}, nil)
	rk := newRelayKey(t, "node-a", 0x11, operator, "10.1.0.1")
	f.register(t, rk, false)
	entry := obs(rk, 10, "1", false)
	// One entry of a two-chunk report stays incomplete, so the resend is not
	// the completed-report short circuit.
	root, err := types.InputsRoot([]types.RelayObservation{entry})
	require.NoError(t, err)
	msg := &types.MsgReportEpoch{
		Reporter:   reporter.String(),
		Epoch:      4,
		ChunkIndex: 0,
		ChunkCount: 2,
		Entries:    []types.RelayObservation{entry},
		InputsRoot: root,
	}
	f.closeEpoch(4)
	first, err := f.Msg.ReportEpoch(f.Ctx, msg)
	require.NoError(t, err)
	require.False(t, first.Complete)
	second, err := f.Msg.ReportEpoch(f.Ctx, msg)
	require.NoError(t, err)
	require.False(t, second.Complete)
}

func TestPayUsesReassembledChunks(t *testing.T) {
	f := newTestFixture(t)
	reporter := acc(1)
	op1, op2 := acc(2), acc(3)
	f.init(t, 1, []sdk.AccAddress{reporter}, nil)
	r1 := newRelayKey(t, "node-a", 0x11, op1, "10.1.0.1")
	r2 := newRelayKey(t, "node-b", 0x22, op2, "10.8.0.1")
	f.register(t, r1, false)
	f.register(t, r2, false)
	f.activate(t, []sdk.AccAddress{reporter}, []types.RelayObservation{obs(r1, 1, "1", false)})

	entries := []types.RelayObservation{obs(r1, 15, "1", false), obs(r2, 25, "1", false)}
	root, err := types.InputsRoot(entries)
	require.NoError(t, err)
	f.closeEpoch(2)
	_, err = f.Msg.ReportEpoch(f.Ctx, &types.MsgReportEpoch{
		Reporter: reporter.String(), Epoch: 2, ChunkIndex: 1, ChunkCount: 2,
		Entries: []types.RelayObservation{entries[1]}, InputsRoot: root,
	})
	require.NoError(t, err)
	_, err = f.Msg.ReportEpoch(f.Ctx, &types.MsgReportEpoch{
		Reporter: reporter.String(), Epoch: 2, ChunkIndex: 0, ChunkCount: 2,
		Entries: []types.RelayObservation{entries[0]}, InputsRoot: root,
	})
	require.NoError(t, err)
	f.Emission.setCeiling(2, math.NewInt(1000))
	result, err := f.Keeper.SettleEpoch(f.Ctx, 2)
	require.NoError(t, err)
	require.True(t, result.Minted.Equal(math.NewInt(40)))
	require.True(t, f.Earnings.balance(op1).Equal(math.NewInt(15)))
	require.True(t, f.Earnings.balance(op2).Equal(math.NewInt(23)))
}
