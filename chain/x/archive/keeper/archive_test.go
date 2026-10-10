package keeper_test

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/cometbft/cometbft/crypto/merkle"
	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/archive"
	"github.com/DeBrosOfficial/network/chain/x/archive/types"
)

func TestAttest_archivedOnlyAfterThreeArchiversAndThreeDeals(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)

	const (
		start = int64(1)
		end   = int64(50)
		cid   = "bafyarchivecid"
	)
	bundle := digest(0x11)
	root := digest(0x22)

	first := f.attest(t, 1, start, end, cid, bundle, root)
	require.False(t, first.Archived)
	require.Equal(t, uint32(1), first.Attesters)

	second := f.attest(t, 2, start, end, cid, bundle, root)
	require.False(t, second.Archived)
	require.Equal(t, uint32(2), second.Attesters)

	// The same archiver repeating the root does not count twice.
	repeat := f.attest(t, 1, start, end, cid, bundle, root)
	require.False(t, repeat.Archived)
	require.Equal(t, uint32(2), repeat.Attesters)

	third := f.attest(t, 3, start, end, cid, bundle, root)
	require.False(t, third.Archived)
	require.Equal(t, uint32(3), third.Attesters)

	twoDeals := f.attach(t, 1, start, end, "1", "2")
	require.False(t, twoDeals.Archived)
	require.Equal(t, uint32(2), twoDeals.Replicas)

	// Re-recording the same ids does not increase the replica count.
	again := f.attach(t, 2, start, end, "1", "2")
	require.False(t, again.Archived)
	require.Equal(t, uint32(2), again.Replicas)

	three := f.attach(t, 3, start, end, "3")
	require.True(t, three.Archived)
	require.Equal(t, uint32(3), three.Replicas)

	rec, err := f.Keeper.GetRange(f.Ctx, start, end)
	require.NoError(t, err)
	require.True(t, rec.Archived)
	require.Equal(t, []string{acc(1).String(), acc(2).String(), acc(3).String()}, rec.Archivers)
	require.Equal(t, []string{"1", "2", "3"}, rec.DealIds)
	require.Equal(t, root, rec.MerkleRoot)

	last, err := f.Keeper.LastArchivedHeight.Get(f.Ctx)
	require.NoError(t, err)
	require.Equal(t, end, last)

	found := false
	for _, ev := range f.Ctx.EventManager().Events() {
		if ev.Type == types.EventTypeArchived {
			found = true
		}
	}
	require.True(t, found, "archiving a range emits archive_archived")
}

// A decided range refuses an attestation of any other tuple, naming the field that differs, and
// records nothing for it.
func TestAttest_aDecidedRangeRefusesAnyOtherTuple(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	bundle, root := digest(0x11), digest(0x21)
	f.attestQuorum(t, 1, 50, "bafyarchivecid", bundle, root)

	for name, tc := range map[string]struct {
		mutate func(*types.MsgAttest)
		want   error
	}{
		"another root":   {func(m *types.MsgAttest) { m.MerkleRoot = digest(0x22) }, types.ErrWrongRoot},
		"another cid":    {func(m *types.MsgAttest) { m.BundleCid = "bafyothercid" }, types.ErrWrongBundle},
		"another hash":   {func(m *types.MsgAttest) { m.BundleHash = digest(0x33) }, types.ErrWrongBundle},
		"another piece":  {func(m *types.MsgAttest) { m.PieceRoot = digest(8) }, types.ErrWrongPiece},
		"another length": {func(m *types.MsgAttest) { m.PieceBytes, m.RealLeafCount, m.PaddedLeafCount = 1024, 1, 1 }, types.ErrWrongPiece},
	} {
		msg := withPiece(&types.MsgAttest{
			Archiver: acc(4).String(), NodeId: nodeOf(4), StartHeight: 1, EndHeight: 50,
			BundleCid: "bafyarchivecid", BundleHash: bundle, MerkleRoot: root,
		})
		tc.mutate(msg)
		_, err := f.Msg.Attest(f.Ctx, msg)
		require.ErrorIsf(t, err, tc.want, "%s", name)
	}
	rec, err := f.Keeper.GetRange(f.Ctx, 1, 50)
	require.NoError(t, err)
	require.Len(t, rec.Archivers, 3, "the refused attestations are not recorded")
	require.Equal(t, root, rec.MerkleRoot)

	// A fourth operator that attests the winning tuple is extra evidence and is accepted.
	res := f.attest(t, 4, 1, 50, "bafyarchivecid", bundle, root)
	require.Equal(t, uint32(4), res.Attesters)
}

// The first attestation of a range does not fix what the range is. Before, a wrong first attester
// pinned its root and every honest archiver was refused for ever.
func TestAttest_aWrongFirstAttestationDoesNotPinTheRange(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	bundle := digest(0x11)
	wrong, right := digest(0x21), digest(0x22)

	f.attest(t, 1, 1, 50, "bafyarchivecid", bundle, wrong)
	res := f.attest(t, 2, 1, 50, "bafyarchivecid", bundle, right)
	require.Equal(t, uint32(1), res.Attesters, "the second archiver disagrees, so it starts its own tally")
	f.attest(t, 3, 1, 50, "bafyarchivecid", bundle, right)
	rec, err := f.Keeper.GetRange(f.Ctx, 1, 50)
	require.NoError(t, err)
	require.False(t, rec.Decided)
	require.Len(t, rec.Candidates, 2, "the two tuples coexist until one wins")

	res = f.attest(t, 4, 1, 50, "bafyarchivecid", bundle, right)
	require.Equal(t, uint32(3), res.Attesters)
	rec, err = f.Keeper.GetRange(f.Ctx, 1, 50)
	require.NoError(t, err)
	require.True(t, rec.Decided)
	require.Equal(t, right, rec.MerkleRoot, "the tuple three operators attested won, not the first one")
	require.Equal(t, []string{acc(2).String(), acc(3).String(), acc(4).String()}, rec.Archivers)
	require.Empty(t, rec.Candidates, "the losing tuple is dropped")

	_, err = f.Msg.Attest(f.Ctx, withPiece(&types.MsgAttest{
		Archiver: acc(1).String(), NodeId: nodeOf(1), StartHeight: 1, EndHeight: 50,
		BundleCid: "bafyarchivecid", BundleHash: bundle, MerkleRoot: wrong,
	}))
	require.ErrorIs(t, err, types.ErrWrongRoot, "the wrong tuple is refused once the range is decided")
}

// Attestations are tallied per full tuple: archivers that differ in any one field of it do not
// count toward each other, so two of one tuple and one of another decide nothing.
func TestAttest_everyFieldOfTheTupleSeparatesTheTally(t *testing.T) {
	variants := map[string]func(*types.MsgAttest){
		"merkle root": func(m *types.MsgAttest) { m.MerkleRoot = digest(0x99) },
		"bundle cid":  func(m *types.MsgAttest) { m.BundleCid = "bafyothercid" },
		"bundle hash": func(m *types.MsgAttest) { m.BundleHash = digest(0x98) },
		"piece root":  func(m *types.MsgAttest) { m.PieceRoot = digest(0x97) },
		"piece size": func(m *types.MsgAttest) {
			m.PieceBytes, m.RealLeafCount, m.PaddedLeafCount = 2048, 2, 2
		},
	}
	for name, mutate := range variants {
		t.Run(name, func(t *testing.T) {
			f := newTestFixture(t)
			f.initGenesis(t, nil)
			f.attest(t, 1, 1, 50, "bafyarchivecid", digest(1), digest(2))
			f.attest(t, 2, 1, 50, "bafyarchivecid", digest(1), digest(2))
			odd := withPiece(&types.MsgAttest{
				Archiver: acc(3).String(), NodeId: nodeOf(3), StartHeight: 1, EndHeight: 50,
				BundleCid: "bafyarchivecid", BundleHash: digest(1), MerkleRoot: digest(2),
			})
			mutate(odd)
			res, err := f.Msg.Attest(f.Ctx, odd)
			require.NoError(t, err)
			require.Equal(t, uint32(1), res.Attesters)
			rec, err := f.Keeper.GetRange(f.Ctx, 1, 50)
			require.NoError(t, err)
			require.False(t, rec.Decided, "two operators on one tuple and one on another decide nothing")
			require.Len(t, rec.Candidates, 2)
			_, err = f.Msg.CreateArchiveDeal(f.Ctx, f.createMsg(1, 1, 50))
			require.ErrorIs(t, err, types.ErrQuorumPending)

			res = f.attest(t, 4, 1, 50, "bafyarchivecid", digest(1), digest(2))
			require.Equal(t, uint32(3), res.Attesters)
			rec, err = f.Keeper.GetRange(f.Ctx, 1, 50)
			require.NoError(t, err)
			require.True(t, rec.Decided)
			require.Equal(t, digest(2), rec.MerkleRoot)
			require.Equal(t, types.Piece{Root: digest(7), RealLeafCount: 3, PaddedLeafCount: 4, PieceBytes: 3000}, rec.PieceOf())
		})
	}
}

// An operator counts toward one tuple per range: it cannot spread itself over several to fill the
// candidate set or to count twice.
func TestAttest_anOperatorCannotAttestTwoTuplesOfARange(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	f.Nodes.operator[nodeOf(2)] = opOf(1)
	f.attest(t, 1, 1, 50, "bafyarchivecid", digest(1), digest(2))

	for _, signer := range []byte{1, 2} {
		_, err := f.Msg.Attest(f.Ctx, withPiece(&types.MsgAttest{
			Archiver: acc(signer).String(), NodeId: nodeOf(signer), StartHeight: 1, EndHeight: 50,
			BundleCid: "bafyarchivecid", BundleHash: digest(1), MerkleRoot: digest(9),
		}))
		require.ErrorIsf(t, err, types.ErrConflictingAttestation, "node %d", signer)
	}
	rec, err := f.Keeper.GetRange(f.Ctx, 1, 50)
	require.NoError(t, err)
	require.Len(t, rec.Candidates, 1)
}

// The candidate set is bounded by max_candidates_per_range: a tuple beyond it is refused, the
// tuples already there keep counting, and one of them can still win.
func TestAttest_theCandidateSetIsBounded(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, func(gs *types.GenesisState) { gs.Params.MaxCandidatesPerRange = 2 })
	f.attest(t, 1, 1, 50, "bafyarchivecid", digest(1), digest(2))
	f.attest(t, 2, 1, 50, "bafyarchivecid", digest(1), digest(3))
	_, err := f.Msg.Attest(f.Ctx, withPiece(&types.MsgAttest{
		Archiver: acc(3).String(), NodeId: nodeOf(3), StartHeight: 1, EndHeight: 50,
		BundleCid: "bafyarchivecid", BundleHash: digest(1), MerkleRoot: digest(4),
	}))
	require.ErrorIs(t, err, types.ErrCandidatesFull)

	f.attest(t, 3, 1, 50, "bafyarchivecid", digest(1), digest(3))
	res := f.attest(t, 4, 1, 50, "bafyarchivecid", digest(1), digest(3))
	require.Equal(t, uint32(3), res.Attesters)
	rec, err := f.Keeper.GetRange(f.Ctx, 1, 50)
	require.NoError(t, err)
	require.True(t, rec.Decided)
	require.Equal(t, digest(3), rec.MerkleRoot)
}

func TestAttachReplicas_unknownRangeAndFutureHeightRefused(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)

	_, err := f.Msg.AttachReplicas(f.Ctx, &types.MsgAttachReplicas{
		Archiver:    acc(1).String(),
		NodeId:      nodeOf(1),
		StartHeight: 1,
		EndHeight:   10,
		DealIds:     []string{"1"},
	})
	require.ErrorIs(t, err, types.ErrUnknownRange)

	_, err = f.Msg.Attest(f.Ctx, withPiece(&types.MsgAttest{
		Archiver:    acc(1).String(),
		NodeId:      nodeOf(1),
		StartHeight: f.Ctx.BlockHeight() - testRangeBlocks + 1,
		EndHeight:   f.Ctx.BlockHeight(),
		BundleCid:   "bafyarchivecid",
		BundleHash:  digest(1),
		MerkleRoot:  digest(2),
	}))
	require.ErrorIs(t, err, types.ErrNotFinalized)
}

// Only the fixed ranges of Params.RangeBlocks are accepted, so an operator cannot squat an arbitrary
// span or a slice of a neighbouring range, and two accepted ranges can never overlap.
func TestAttest_onlyCanonicalRangesAreAccepted(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	attest := func(start, end int64) error {
		_, err := f.Msg.Attest(f.Ctx, withPiece(&types.MsgAttest{
			Archiver: acc(1).String(), NodeId: nodeOf(1), StartHeight: start, EndHeight: end,
			BundleCid: "bafyarchivecid", BundleHash: digest(1), MerkleRoot: digest(2),
		}))
		return err
	}
	for name, r := range map[string][2]int64{
		"twice as wide":            {1, 100},
		"shifted by one":           {2, 51},
		"squatting a slice":        {25, 74},
		"narrower than the width":  {1, 25},
		"a single height":          {51, 51},
		"overlaps its predecessor": {50, 99},
	} {
		require.ErrorIs(t, attest(r[0], r[1]), types.ErrNotCanonicalRange, name)
	}
	require.NoError(t, attest(1, 50))
	require.NoError(t, attest(51, 100))
	require.NoError(t, attest(1_001, 1_050), "a range far ahead is canonical too")
	_, err := f.Keeper.GetRange(f.Ctx, 25, 74)
	require.ErrorIs(t, err, types.ErrUnknownRange)
}

func TestAttest_rangeWidthFollowsTheGenesisParam(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, func(gs *types.GenesisState) { gs.Params.RangeBlocks = 10 })
	f.attest(t, 1, 11, 20, "bafyarchivecid", digest(1), digest(2))
	_, err := f.Msg.Attest(f.Ctx, withPiece(&types.MsgAttest{
		Archiver: acc(1).String(), NodeId: nodeOf(1), StartHeight: 1, EndHeight: 50,
		BundleCid: "bafyarchivecid", BundleHash: digest(1), MerkleRoot: digest(2),
	}))
	require.ErrorIs(t, err, types.ErrNotCanonicalRange)
}

func TestLastArchivedHeight_gapDoesNotJump(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)

	archiveRange(t, f, 1, 50)
	last, err := f.Keeper.LastArchivedHeight.Get(f.Ctx)
	require.NoError(t, err)
	require.Equal(t, int64(50), last)

	archiveRange(t, f, 101, 150)
	last, err = f.Keeper.LastArchivedHeight.Get(f.Ctx)
	require.NoError(t, err)
	require.Equal(t, int64(50), last)

	archiveRange(t, f, 51, 100)
	last, err = f.Keeper.LastArchivedHeight.Get(f.Ctx)
	require.NoError(t, err)
	require.Equal(t, int64(150), last)
}

func TestRetainHeight_stallForAYearStaysAtLastArchived(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	archiveRange(t, f, 1, 50)

	blocks := types.DefaultBlocksIn14Days
	year := blocks * 365 / 14
	tip := int64(50) + year
	ctx := f.Ctx.WithBlockHeight(tip)

	retain, err := f.Keeper.RetainHeight(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(50), retain)
	require.Equal(t, retain, archive.RetainHeight(tip, blocks, 50))
	require.LessOrEqual(t, retain, int64(50))

	naive := tip - blocks
	require.Greater(t, naive, int64(50), "a year ahead, the 14-day window is far past the archive")

	// Pruning the last archived block, or anything past it, is refused.
	for _, height := range []int64{50, 51, naive - 1, tip - 1} {
		ok, err := f.Keeper.PruneAllowed(ctx, height)
		require.NoError(t, err)
		require.False(t, ok, "height %d", height)
	}
	ok, err := f.Keeper.PruneAllowed(ctx, 49)
	require.NoError(t, err)
	require.True(t, ok)

	// Nothing is archived above 50, so a node must not prune there even one block later.
	ok, err = f.Keeper.PruneAllowed(ctx, 50)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestRetainHeight_fourteenDayWindowBindsWhenArchiveIsCaughtUp(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, func(gs *types.GenesisState) {
		gs.Params.RetentionWindowBlocks = types.MinBlocksIn14Days
		gs.Params.RangeBlocks = types.MaxRangeBlocksLimit
	})
	const ranges = 25
	tip := ranges*types.MaxRangeBlocksLimit + 1
	f.Ctx = f.Ctx.WithBlockHeight(tip)
	for i := int64(0); i < ranges; i++ {
		archiveRange(t, f, i*types.MaxRangeBlocksLimit+1, (i+1)*types.MaxRangeBlocksLimit)
	}
	ctx := f.Ctx

	retain, err := f.Keeper.RetainHeight(ctx)
	require.NoError(t, err)
	want := tip - types.MinBlocksIn14Days
	require.Equal(t, want, retain)
	require.LessOrEqual(t, retain, tip-1)

	ok, err := f.Keeper.PruneAllowed(ctx, want)
	require.NoError(t, err)
	require.False(t, ok, "the block at the retain height is kept")
	ok, err = f.Keeper.PruneAllowed(ctx, want-1)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = f.Keeper.PruneAllowed(ctx, tip-1)
	require.NoError(t, err)
	require.False(t, ok, "archived blocks inside the 14-day window are kept")
}

func TestVerifyBundle_mutatedHeaderFails(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)

	blockHashes := make([][]byte, testRangeBlocks)
	for i := range blockHashes {
		blockHashes[i] = digest(byte(i + 1))
	}
	root := merkle.HashFromByteSlices(blockHashes)
	bundle := digest(9)
	require.NotEqual(t, bundle, root)

	const (
		start = int64(1)
		end   = testRangeBlocks
		cid   = "bafyarchivecid"
	)
	for i := byte(1); i <= 3; i++ {
		f.attest(t, i, start, end, cid, bundle, root)
	}
	f.attach(t, 1, start, end, "1", "2", "3")

	require.NoError(t, f.Keeper.VerifyBundle(f.Ctx, start, end, blockHashes, bundle))

	mutated := append([][]byte(nil), blockHashes...)
	flipped := append([]byte(nil), mutated[1]...)
	flipped[0] ^= 0xff
	mutated[1] = flipped
	err := f.Keeper.VerifyBundle(f.Ctx, start, end, mutated, bundle)
	require.ErrorIs(t, err, types.ErrWrongRoot)

	err = f.Keeper.VerifyBundle(f.Ctx, start, end, blockHashes, digest(8))
	require.ErrorIs(t, err, types.ErrWrongBundle)
}

func TestQuery_rangeAndRetainHeight(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	archiveRange(t, f, 1, 50)

	params, err := f.Query.Params(f.Ctx, &types.QueryParamsRequest{})
	require.NoError(t, err)
	require.Equal(t, types.DefaultBlocksIn14Days, params.Params.RetentionWindowBlocks)

	got, err := f.Query.Range(f.Ctx, &types.QueryRangeRequest{StartHeight: 1, EndHeight: 50})
	require.NoError(t, err)
	require.True(t, got.Range.Archived)

	last, err := f.Query.LastArchivedHeight(f.Ctx, &types.QueryLastArchivedHeightRequest{})
	require.NoError(t, err)
	require.Equal(t, int64(50), last.LastArchivedHeight)

	retain, err := f.Query.RetainHeight(f.Ctx, &types.QueryRetainHeightRequest{})
	require.NoError(t, err)
	require.Equal(t, int64(50), retain.LastArchivedHeight)
	require.Equal(t, f.Ctx.BlockHeight(), retain.Tip)
	require.LessOrEqual(t, retain.RetainHeight, retain.LastArchivedHeight)
}

func TestMsgSignerIsArchiver(t *testing.T) {
	signer := acc(7)
	attest := withPiece(&types.MsgAttest{
		Archiver:    signer.String(),
		StartHeight: 1,
		EndHeight:   2,
		BundleCid:   "bafyarchivecid",
		BundleHash:  digest(1),
		MerkleRoot:  digest(2),
	})
	require.Equal(t, []sdk.AccAddress{signer}, attest.GetSigners())

	attach := &types.MsgAttachReplicas{
		Archiver:    signer.String(),
		NodeId:      nodeOf(7),
		StartHeight: 1,
		EndHeight:   2,
		DealIds:     []string{"1"},
	}
	require.Equal(t, []sdk.AccAddress{signer}, attach.GetSigners())
	require.NoError(t, attach.ValidateBasic())
	attach.NodeId = ""
	require.Error(t, attach.ValidateBasic(), "a message must name its archiver node")
}

func archiveRange(t *testing.T, f *testFixture, start, end int64) {
	t.Helper()
	cid := "bafyarchivecid"
	bundle := digest(0x41)
	root := digest(0x42)
	for i := byte(1); i <= 3; i++ {
		f.attest(t, i, start, end, cid, bundle, root)
	}
	res := f.attach(t, 1, start, end, fmt.Sprint(start*10+1), fmt.Sprint(start*10+2), fmt.Sprint(start*10+3))
	require.True(t, res.Archived)
}

func TestExportGenesis_roundTrip(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	archiveRange(t, f, 1, 50)

	exported, err := f.Keeper.ExportGenesis(f.Ctx)
	require.NoError(t, err)
	require.NoError(t, exported.Validate())
	require.Equal(t, int64(50), exported.LastArchivedHeight)
	require.Len(t, exported.Ranges, 1)

	f2 := newTestFixture(t)
	require.NoError(t, f2.Keeper.InitGenesis(f2.Ctx, *exported))
	again, err := f2.Keeper.ExportGenesis(f2.Ctx)
	require.NoError(t, err)
	require.Equal(t, exported.LastArchivedHeight, again.LastArchivedHeight)
	require.Equal(t, exported.Ranges[0].Archivers, again.Ranges[0].Archivers)
	require.True(t, bytes.Equal(exported.Ranges[0].MerkleRoot, again.Ranges[0].MerkleRoot))
}

func TestAttest_onlyAnArchiverNodesHotKeyCounts(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	msg := withPiece(&types.MsgAttest{
		Archiver: acc(1).String(), NodeId: nodeOf(2), StartHeight: 1, EndHeight: 50,
		BundleCid: "bafyarchivecid", BundleHash: digest(1), MerkleRoot: digest(2),
	})
	_, err := f.Msg.Attest(f.Ctx, msg)
	require.ErrorContains(t, err, "not the hot key", "another node's id does not make acc(1) an archiver")

	msg.NodeId = ""
	_, err = f.Msg.Attest(f.Ctx, msg)
	require.Error(t, err, "an attestation must name its node")

	f.Nodes.inactive[nodeOf(1)] = true
	msg.NodeId = nodeOf(1)
	_, err = f.Msg.Attest(f.Ctx, msg)
	require.ErrorContains(t, err, "ARCHIVER", "a node without an active ARCHIVER bond cannot attest")
	_, err = f.Keeper.GetRange(f.Ctx, 1, 50)
	require.ErrorIs(t, err, types.ErrUnknownRange, "a refused attestation records nothing")
}

// A second node of the same operator, or the same operator after a hot-key
// rotation, is accepted and counts nothing: the archiver keeps moving and the
// operator still counts once.
func TestAttest_oneOperatorsNodesCountOnce(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	f.Nodes.operator[nodeOf(2)] = opOf(1)
	f.attest(t, 1, 1, 50, "bafyarchivecid", digest(1), digest(2))
	res := f.attest(t, 2, 1, 50, "bafyarchivecid", digest(1), digest(2))
	require.Equal(t, uint32(1), res.Attesters)
	rec, err := f.Keeper.GetRange(f.Ctx, 1, 50)
	require.NoError(t, err)
	require.Len(t, rec.Candidates, 1)
	require.Equal(t, []string{opOf(1)}, rec.Candidates[0].Operators)
	require.Equal(t, []string{acc(1).String()}, rec.Candidates[0].Archivers)

	// The same operator's node with another tuple is refused.
	_, err = f.Msg.Attest(f.Ctx, withPiece(&types.MsgAttest{
		Archiver: acc(2).String(), NodeId: nodeOf(2), StartHeight: 1, EndHeight: 50,
		BundleCid: "bafyarchivecid", BundleHash: digest(1), MerkleRoot: digest(9),
	}))
	require.ErrorIs(t, err, types.ErrConflictingAttestation)
}

// A key that already attested is idempotent even after its node lost the role.
func TestAttest_repeatAfterLosingTheRoleIsIdempotent(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	f.attest(t, 1, 1, 50, "bafyarchivecid", digest(1), digest(2))
	f.Nodes.inactive[nodeOf(1)] = true
	res := f.attest(t, 1, 1, 50, "bafyarchivecid", digest(1), digest(2))
	require.Equal(t, uint32(1), res.Attesters)
	f.Nodes.inactive[nodeOf(1)] = false
	for _, signer := range []byte{2, 3} {
		f.attest(t, signer, 1, 50, "bafyarchivecid", digest(1), digest(2))
	}
	f.Nodes.inactive[nodeOf(1)] = true
	res = f.attest(t, 1, 1, 50, "bafyarchivecid", digest(1), digest(2))
	require.Equal(t, uint32(3), res.Attesters, "the same holds once the range is decided")
}

// Deals can only back a tuple that has won the range.
func TestAttachReplicas_needsAWinningTuple(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	f.attest(t, 1, 1, 50, "bafyarchivecid", digest(1), digest(2))
	f.attest(t, 2, 1, 50, "bafyarchivecid", digest(1), digest(2))
	_, err := f.Msg.AttachReplicas(f.Ctx, &types.MsgAttachReplicas{
		Archiver: acc(1).String(), NodeId: nodeOf(1), StartHeight: 1, EndHeight: 50, DealIds: []string{"1"},
	})
	require.ErrorIs(t, err, types.ErrQuorumPending)
}

func TestAttachReplicas_onlyActiveArchiveDeals(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	f.attestBy(t, []byte{1, 2, 4}, 1, 50, "bafyarchivecid", digest(1), digest(2))
	for _, id := range []string{"deal-1", "0", "-1", "01"} {
		_, err := f.Msg.AttachReplicas(f.Ctx, &types.MsgAttachReplicas{
			Archiver: acc(1).String(), NodeId: nodeOf(1), StartHeight: 1, EndHeight: 50, DealIds: []string{"1", id},
		})
		require.ErrorContainsf(t, err, "not a decimal", "id %q", id)
	}
	_, err := f.Msg.AttachReplicas(f.Ctx, &types.MsgAttachReplicas{
		Archiver: acc(1).String(), NodeId: nodeOf(1), StartHeight: 1, EndHeight: 50, DealIds: []string{"1", "10"},
	})
	require.ErrorIs(t, err, types.ErrNotArchiveDeal)
	rec, err := f.Keeper.GetRange(f.Ctx, 1, 50)
	require.NoError(t, err)
	require.Empty(t, rec.DealIds, "a refused attach records none of its ids")
	_, err = f.Msg.AttachReplicas(f.Ctx, &types.MsgAttachReplicas{
		Archiver: acc(3).String(), NodeId: nodeOf(3), StartHeight: 1, EndHeight: 50, DealIds: []string{"1"},
	})
	require.ErrorIs(t, err, types.ErrNotAttester, "an archiver that did not attest the range cannot claim deals for it")
	f.attach(t, 1, 1, 50, "1")

	// A deal backs one range only.
	f.attestBy(t, []byte{1, 2, 4}, 51, 100, "bafyarchivecid", digest(1), digest(3))
	_, err = f.Msg.AttachReplicas(f.Ctx, &types.MsgAttachReplicas{
		Archiver: acc(1).String(), NodeId: nodeOf(1), StartHeight: 51, EndHeight: 100, DealIds: []string{"1"},
	})
	require.ErrorIs(t, err, types.ErrDealAttached)
}

// A deal that ended after it was attached does not count when the range
// would become archived.
func TestAttach_anEndedDealDoesNotCountTowardArchiving(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	for n := byte(1); n <= 3; n++ {
		f.attest(t, n, 1, 50, "bafyarchivecid", digest(1), digest(2))
	}
	f.attach(t, 1, 1, 50, "1")
	f.Storage.ended[1] = true
	res := f.attach(t, 1, 1, 50, "2", "3")
	require.False(t, res.Archived, "deal 1 ended, so only two live replicas")
	res = f.attach(t, 1, 1, 50, "4")
	require.True(t, res.Archived)
}

// A range whose early deal ended keeps a record that exports and imports: the
// ended deal is dropped and freed, and the index is rebuilt on import.
func TestExportGenesis_roundTripsAnEndedDealAndTheIndex(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	for n := byte(1); n <= 3; n++ {
		f.attest(t, n, 1, 50, "bafyarchivecid", digest(1), digest(2))
	}
	f.attach(t, 1, 1, 50, "1")
	f.Storage.ended[1] = true
	res := f.attach(t, 1, 1, 50, "2", "3")
	require.False(t, res.Archived)
	rec, err := f.Keeper.GetRange(f.Ctx, 1, 50)
	require.NoError(t, err)
	require.Equal(t, []string{"2", "3"}, rec.DealIds, "the ended deal was dropped")

	gs, err := f.Keeper.ExportGenesis(f.Ctx)
	require.NoError(t, err)
	require.NoError(t, gs.Validate())
	g := newTestFixture(t)
	require.NoError(t, g.Keeper.InitGenesis(g.Ctx, *gs))
	imported, err := g.Keeper.GetRange(g.Ctx, 1, 50)
	require.NoError(t, err)
	require.Equal(t, rec.Operators, imported.Operators)
	require.Equal(t, rec.DealIds, imported.DealIds)

	g.attestQuorum(t, 51, 100, "bafyarchivecid", digest(1), digest(3))
	_, err = g.Msg.AttachReplicas(g.Ctx, &types.MsgAttachReplicas{
		Archiver: acc(1).String(), NodeId: nodeOf(1), StartHeight: 51, EndHeight: 100, DealIds: []string{"2"},
	})
	require.ErrorIs(t, err, types.ErrDealAttached, "the imported index still holds deal 2")
}

func (f *testFixture) createDeal(t *testing.T, signer byte, start, end int64) uint64 {
	t.Helper()
	res, err := f.Msg.CreateArchiveDeal(f.Ctx, f.createMsg(signer, start, end))
	require.NoError(t, err)
	return res.DealId
}

func (f *testFixture) createMsg(signer byte, start, end int64) *types.MsgCreateArchiveDeal {
	return &types.MsgCreateArchiveDeal{
		Archiver: acc(signer).String(), NodeId: nodeOf(signer), StartHeight: start, EndHeight: end,
		PieceRoot: digest(7), RealLeafCount: 3, PaddedLeafCount: 4, PieceBytes: 3000,
	}
}

func TestCreateArchiveDeal_opensAChainPricedDealThatCountsOnlyOnceItHasAProvider(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	for n := byte(1); n <= 3; n++ {
		f.attest(t, n, 1, 50, "bafyarchivecid", digest(1), digest(2))
	}
	var ids []string
	for i := 0; i < types.MinReplicaDeals; i++ {
		ids = append(ids, fmt.Sprint(f.createDeal(t, 1, 1, 50)))
	}
	require.Len(t, f.Storage.opened, types.MinReplicaDeals)
	require.Equal(t, types.ArchiveDealEpochs, f.Storage.opened[0].Duration, "the chain, not the archiver, sets the duration")
	require.Equal(t, digest(7), f.Storage.opened[0].Root)

	_, err := f.Msg.AttachReplicas(f.Ctx, &types.MsgAttachReplicas{
		Archiver: acc(1).String(), NodeId: nodeOf(1), StartHeight: 1, EndHeight: 50, DealIds: ids,
	})
	require.ErrorIs(t, err, types.ErrNotArchiveDeal, "an OPEN deal has no provider and cannot make a range archived")
	rec, err := f.Keeper.GetRange(f.Ctx, 1, 50)
	require.NoError(t, err)
	require.False(t, rec.Archived)

	f.Storage.activate(firstOpened, firstOpened+1, firstOpened+2)
	res := f.attach(t, 1, 1, 50, ids...)
	require.True(t, res.Archived)
	last, err := f.Keeper.LastArchivedHeight.Get(f.Ctx)
	require.NoError(t, err)
	require.Equal(t, int64(50), last)
}

func TestCreateArchiveDeal_aRangeHoldsAtMostItsQuorumOfLiveDeals(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	f.attestQuorum(t, 1, 50, "bafyarchivecid", digest(1), digest(2))
	for i := 0; i < types.MaxLiveDealsPerRange; i++ {
		f.createDeal(t, 1, 1, 50)
	}
	_, err := f.Msg.CreateArchiveDeal(f.Ctx, f.createMsg(1, 1, 50))
	require.ErrorIs(t, err, types.ErrDealsFull)
	require.Len(t, f.Storage.opened, types.MaxLiveDealsPerRange, "the refused message opened no deal")

	f.Storage.ended[firstOpened] = true
	f.createDeal(t, 1, 1, 50)
	require.Len(t, f.Storage.opened, types.MaxLiveDealsPerRange+1, "a pending deal that ended frees its place")
	_, err = f.Msg.CreateArchiveDeal(f.Ctx, f.createMsg(1, 1, 50))
	require.ErrorIs(t, err, types.ErrDealsFull)
}

func TestCreateArchiveDeal_recordedLiveDealsCountAndEndedOnesAreRenewed(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	f.attestQuorum(t, 1, 50, "bafyarchivecid", digest(1), digest(2))
	f.attach(t, 1, 1, 50, "1", "2", "3")
	_, err := f.Msg.CreateArchiveDeal(f.Ctx, f.createMsg(1, 1, 50))
	require.ErrorIs(t, err, types.ErrDealsFull, "three recorded live deals are all a range may have")

	f.Storage.ended[2] = true
	id := f.createDeal(t, 1, 1, 50)
	rec, err := f.Keeper.GetRange(f.Ctx, 1, 50)
	require.NoError(t, err)
	require.Equal(t, []string{"1", "3"}, rec.DealIds, "the ended deal is dropped")
	require.Equal(t, firstOpened, id)
}

func TestCreateArchiveDeal_onlyAnAttesterOfTheRangeMayAskForDeals(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	f.attestBy(t, []byte{1, 2, 4}, 1, 50, "bafyarchivecid", digest(1), digest(2))

	_, err := f.Msg.CreateArchiveDeal(f.Ctx, f.createMsg(3, 1, 50))
	require.ErrorIs(t, err, types.ErrNotAttester)

	wrongKey := f.createMsg(1, 1, 50)
	wrongKey.Archiver = acc(9).String()
	_, err = f.Msg.CreateArchiveDeal(f.Ctx, wrongKey)
	require.ErrorContains(t, err, "not the hot key")

	f.Nodes.inactive[nodeOf(1)] = true
	_, err = f.Msg.CreateArchiveDeal(f.Ctx, f.createMsg(1, 1, 50))
	require.ErrorContains(t, err, "no active ARCHIVER role")
	require.Empty(t, f.Storage.opened, "no refused message opened a deal")
}

func TestCreateArchiveDeal_unknownAndUnfinalizedRangesAreRefused(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	_, err := f.Msg.CreateArchiveDeal(f.Ctx, f.createMsg(1, 1, 50))
	require.ErrorIs(t, err, types.ErrUnknownRange)
	_, err = f.Msg.CreateArchiveDeal(f.Ctx, f.createMsg(1, 1, 1_000_000))
	require.ErrorIs(t, err, types.ErrNotFinalized)
	require.Empty(t, f.Storage.opened)
}

func TestCreateArchiveDeal_aPendingDealBacksNoOtherRange(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	f.attestQuorum(t, 1, 50, "bafyarchivecid", digest(1), digest(2))
	f.attestQuorum(t, 51, 100, "bafyarchivecid", digest(1), digest(3))
	id := f.createDeal(t, 1, 1, 50)
	f.Storage.activate(id)
	_, err := f.Msg.AttachReplicas(f.Ctx, &types.MsgAttachReplicas{
		Archiver: acc(1).String(), NodeId: nodeOf(1), StartHeight: 51, EndHeight: 100, DealIds: []string{fmt.Sprint(id)},
	})
	require.ErrorIs(t, err, types.ErrDealAttached)
}

// One archiver must not be able to open deals for content the attesters did not agree on, or
// before they agreed on anything: deals wait for the archived quorum of operators, and every
// deal opens with the commitment the range pinned.
func TestCreateArchiveDeal_waitsForTheAttestationQuorum(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	f.attest(t, 1, 1, 50, "bafyarchivecid", digest(1), digest(2))
	_, err := f.Msg.CreateArchiveDeal(f.Ctx, f.createMsg(1, 1, 50))
	require.ErrorIs(t, err, types.ErrQuorumPending)
	f.attest(t, 2, 1, 50, "bafyarchivecid", digest(1), digest(2))
	_, err = f.Msg.CreateArchiveDeal(f.Ctx, f.createMsg(1, 1, 50))
	require.ErrorIs(t, err, types.ErrQuorumPending, "two operators are short of the three that archive a range")
	require.Empty(t, f.Storage.opened, "a refused message opened no deal")

	f.attest(t, 3, 1, 50, "bafyarchivecid", digest(1), digest(2))
	f.createDeal(t, 1, 1, 50)
	require.Len(t, f.Storage.opened, 1)
}

func TestCreateArchiveDeal_mustMatchThePinnedPiece(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	f.attestQuorum(t, 1, 50, "bafyarchivecid", digest(1), digest(2))

	for name, mutate := range map[string]func(*types.MsgCreateArchiveDeal){
		"another root":   func(m *types.MsgCreateArchiveDeal) { m.PieceRoot = digest(9) },
		"a smaller file": func(m *types.MsgCreateArchiveDeal) { m.PieceBytes, m.RealLeafCount, m.PaddedLeafCount = 1024, 1, 1 },
		"a larger valid file": func(m *types.MsgCreateArchiveDeal) {
			m.PieceBytes, m.RealLeafCount, m.PaddedLeafCount = 1<<30, 1<<20, 1<<20
		},
	} {
		msg := f.createMsg(1, 1, 50)
		mutate(msg)
		_, err := f.Msg.CreateArchiveDeal(f.Ctx, msg)
		require.ErrorIsf(t, err, types.ErrWrongPiece, "%s", name)
	}
	require.Empty(t, f.Storage.opened, "no mismatching message opened a deal")

	f.createDeal(t, 1, 1, 50)
	require.Equal(t, digest(7), f.Storage.opened[0].Root, "the deal opens with the pinned commitment")
	require.Equal(t, uint64(3000), f.Storage.opened[0].Bytes)
}

// Deals open with the piece commitment of the winning tuple, never with the first attester's:
// a first attester that committed to other bytes gets no deal of its own.
func TestCreateArchiveDeal_opensFromTheWinningTuple(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	junk := withPiece(&types.MsgAttest{
		Archiver: acc(1).String(), NodeId: nodeOf(1), StartHeight: 1, EndHeight: 50,
		BundleCid: "bafyjunk", BundleHash: digest(1), MerkleRoot: digest(2),
	})
	junk.PieceRoot = digest(8)
	_, err := f.Msg.Attest(f.Ctx, junk)
	require.NoError(t, err)
	for _, signer := range []byte{2, 3, 4} {
		f.attest(t, signer, 1, 50, "bafyarchivecid", digest(1), digest(2))
	}
	rec, err := f.Keeper.GetRange(f.Ctx, 1, 50)
	require.NoError(t, err)
	require.True(t, rec.Decided)
	require.Equal(t, types.Piece{Root: digest(7), RealLeafCount: 3, PaddedLeafCount: 4, PieceBytes: 3000}, rec.PieceOf())

	// The first attester lost: it is not among the winners and cannot ask for deals.
	_, err = f.Msg.CreateArchiveDeal(f.Ctx, f.createMsg(1, 1, 50))
	require.ErrorIs(t, err, types.ErrNotAttester)
	lost := f.createMsg(2, 1, 50)
	lost.PieceRoot = digest(8)
	_, err = f.Msg.CreateArchiveDeal(f.Ctx, lost)
	require.ErrorIs(t, err, types.ErrWrongPiece, "a deal cannot be opened for the losing tuple's bytes")

	f.createDeal(t, 2, 1, 50)
	require.Equal(t, digest(7), f.Storage.opened[0].Root, "the deal opens with the winning commitment")
}

func TestAttest_pieceLargerThanTheCapIsRefused(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, func(gs *types.GenesisState) { gs.Params.MaxPieceBytes = 2999 })
	msg := withPiece(&types.MsgAttest{
		Archiver: acc(1).String(), NodeId: nodeOf(1), StartHeight: 1, EndHeight: 50,
		BundleCid: "bafyarchivecid", BundleHash: digest(1), MerkleRoot: digest(2),
	})
	_, err := f.Msg.Attest(f.Ctx, msg)
	require.ErrorIs(t, err, types.ErrPieceTooLarge)
	_, err = f.Keeper.GetRange(f.Ctx, 1, 50)
	require.ErrorIs(t, err, types.ErrUnknownRange, "a refused attestation records nothing")

	msg.PieceBytes, msg.RealLeafCount, msg.PaddedLeafCount = 2048, 2, 2
	_, err = f.Msg.Attest(f.Ctx, msg)
	require.NoError(t, err)
}

func TestAttest_anUnshapedPieceIsRefused(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	msg := withPiece(&types.MsgAttest{
		Archiver: acc(1).String(), NodeId: nodeOf(1), StartHeight: 1, EndHeight: 50,
		BundleCid: "bafyarchivecid", BundleHash: digest(1), MerkleRoot: digest(2),
	})
	msg.PieceRoot = nil
	_, err := f.Msg.Attest(f.Ctx, msg)
	require.ErrorContains(t, err, "piece_root")
}

// A candidate whose every attesting node has left frees its slot the next time the range is
// attested, so departed operators cannot hold the candidate slots of an undecided range, and the
// operators it held are free to attest another tuple.
func TestAttest_aCandidateOfDepartedArchiversIsFreed(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, func(gs *types.GenesisState) { gs.Params.MaxCandidatesPerRange = 1 })
	f.attest(t, 1, 1, 50, "bafyarchivecid", digest(1), digest(2))

	_, err := f.Msg.Attest(f.Ctx, withPiece(&types.MsgAttest{
		Archiver: acc(2).String(), NodeId: nodeOf(2), StartHeight: 1, EndHeight: 50,
		BundleCid: "bafyarchivecid", BundleHash: digest(1), MerkleRoot: digest(3),
	}))
	require.ErrorIs(t, err, types.ErrCandidatesFull, "while its archiver is active the candidate keeps its slot")

	f.Nodes.inactive[nodeOf(1)] = true
	f.attest(t, 2, 1, 50, "bafyarchivecid", digest(1), digest(3))

	rec, err := f.Keeper.GetRange(f.Ctx, 1, 50)
	require.NoError(t, err)
	require.Len(t, rec.Candidates, 1)
	require.Equal(t, digest(3), rec.Candidates[0].MerkleRoot, "the departed operator's tuple is gone")
	require.Equal(t, []string{nodeOf(2)}, rec.Candidates[0].NodeIds)

	f.Nodes.inactive[nodeOf(1)] = false
	_, err = f.Msg.Attest(f.Ctx, withPiece(&types.MsgAttest{
		Archiver: acc(1).String(), NodeId: nodeOf(1), StartHeight: 1, EndHeight: 50,
		BundleCid: "bafyarchivecid", BundleHash: digest(1), MerkleRoot: digest(3),
	}))
	require.NoError(t, err, "an operator freed from its old tuple can attest the tuple the others hold")
}

// A candidate keeps its slot while any one of its attesting nodes still has the role.
func TestAttest_aCandidateWithOneActiveArchiverKeepsItsSlot(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, func(gs *types.GenesisState) { gs.Params.MaxCandidatesPerRange = 1 })
	f.attest(t, 1, 1, 50, "bafyarchivecid", digest(1), digest(2))
	f.attest(t, 2, 1, 50, "bafyarchivecid", digest(1), digest(2))
	f.Nodes.inactive[nodeOf(1)] = true

	_, err := f.Msg.Attest(f.Ctx, withPiece(&types.MsgAttest{
		Archiver: acc(4).String(), NodeId: nodeOf(4), StartHeight: 1, EndHeight: 50,
		BundleCid: "bafyarchivecid", BundleHash: digest(1), MerkleRoot: digest(3),
	}))
	require.ErrorIs(t, err, types.ErrCandidatesFull)
	rec, err := f.Keeper.GetRange(f.Ctx, 1, 50)
	require.NoError(t, err)
	require.Len(t, rec.Candidates[0].NodeIds, 2)
}
