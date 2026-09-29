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
		end   = int64(100)
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

func TestAttest_wrongRootRefusedAndDoesNotCount(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)

	bundle := digest(0x11)
	rootA := digest(0x21)
	rootB := digest(0x22)
	f.attest(t, 1, 1, 50, "bafyarchivecid", bundle, rootA)

	_, err := f.Msg.Attest(f.Ctx, &types.MsgAttest{
		Archiver:    acc(2).String(),
		NodeId:      nodeOf(2),
		StartHeight: 1,
		EndHeight:   50,
		BundleCid:   "bafyarchivecid",
		BundleHash:  bundle,
		MerkleRoot:  rootB,
	})
	require.ErrorIs(t, err, types.ErrWrongRoot)

	_, err = f.Msg.Attest(f.Ctx, &types.MsgAttest{
		Archiver:    acc(3).String(),
		NodeId:      nodeOf(3),
		StartHeight: 1,
		EndHeight:   50,
		BundleCid:   "bafyothercid",
		BundleHash:  bundle,
		MerkleRoot:  rootA,
	})
	require.ErrorIs(t, err, types.ErrWrongBundle)

	_, err = f.Msg.Attest(f.Ctx, &types.MsgAttest{
		Archiver:    acc(4).String(),
		NodeId:      nodeOf(4),
		StartHeight: 1,
		EndHeight:   50,
		BundleCid:   "bafyarchivecid",
		BundleHash:  digest(0x33),
		MerkleRoot:  rootA,
	})
	require.ErrorIs(t, err, types.ErrWrongBundle)

	rec, err := f.Keeper.GetRange(f.Ctx, 1, 50)
	require.NoError(t, err)
	require.Equal(t, []string{acc(1).String()}, rec.Archivers)
	require.Equal(t, rootA, rec.MerkleRoot)
	require.False(t, rec.Archived)

	// Two more matching attestations still reach 3, not 5: the refused roots did not count.
	f.attest(t, 2, 1, 50, "bafyarchivecid", bundle, rootA)
	res := f.attest(t, 3, 1, 50, "bafyarchivecid", bundle, rootA)
	require.Equal(t, uint32(3), res.Attesters)
	require.False(t, res.Archived)
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

	_, err = f.Msg.Attest(f.Ctx, &types.MsgAttest{
		Archiver:    acc(1).String(),
		NodeId:      nodeOf(1),
		StartHeight: 1,
		EndHeight:   f.Ctx.BlockHeight(),
		BundleCid:   "bafyarchivecid",
		BundleHash:  digest(1),
		MerkleRoot:  digest(2),
	})
	require.ErrorIs(t, err, types.ErrNotFinalized)
}

func TestAttest_overlapRefused(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	f.attest(t, 1, 1, 100, "bafyarchivecid", digest(1), digest(2))

	_, err := f.Msg.Attest(f.Ctx, &types.MsgAttest{
		Archiver:    acc(2).String(),
		NodeId:      nodeOf(2),
		StartHeight: 100,
		EndHeight:   150,
		BundleCid:   "bafyarchivecid",
		BundleHash:  digest(1),
		MerkleRoot:  digest(2),
	})
	require.ErrorIs(t, err, types.ErrOverlap)
}

func TestLastArchivedHeight_gapDoesNotJump(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)

	archiveRange(t, f, 1, 100)
	last, err := f.Keeper.LastArchivedHeight.Get(f.Ctx)
	require.NoError(t, err)
	require.Equal(t, int64(100), last)

	archiveRange(t, f, 201, 300)
	last, err = f.Keeper.LastArchivedHeight.Get(f.Ctx)
	require.NoError(t, err)
	require.Equal(t, int64(100), last)

	archiveRange(t, f, 101, 200)
	last, err = f.Keeper.LastArchivedHeight.Get(f.Ctx)
	require.NoError(t, err)
	require.Equal(t, int64(300), last)
}

func TestRetainHeight_stallForAYearStaysAtLastArchived(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	archiveRange(t, f, 1, 100)

	blocks := types.DefaultBlocksIn14Days
	year := blocks * 365 / 14
	tip := int64(100) + year
	ctx := f.Ctx.WithBlockHeight(tip)

	retain, err := f.Keeper.RetainHeight(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(100), retain)
	require.Equal(t, retain, archive.RetainHeight(tip, blocks, 100))
	require.LessOrEqual(t, retain, int64(100))

	naive := tip - blocks
	require.Greater(t, naive, int64(100), "a year ahead, the 14-day window is far past the archive")

	// Pruning the last archived block, or anything past it, is refused.
	for _, height := range []int64{100, 101, naive - 1, tip - 1} {
		ok, err := f.Keeper.PruneAllowed(ctx, height)
		require.NoError(t, err)
		require.False(t, ok, "height %d", height)
	}
	ok, err := f.Keeper.PruneAllowed(ctx, 99)
	require.NoError(t, err)
	require.True(t, ok)

	// Nothing is archived above 100, so a node must not prune there even one block later.
	ok, err = f.Keeper.PruneAllowed(ctx, 100)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestRetainHeight_fourteenDayWindowBindsWhenArchiveIsCaughtUp(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, func(gs *types.GenesisState) {
		gs.Params.RetentionWindowBlocks = types.MinBlocksIn14Days
	})
	tip := int64(1_000_000)
	archiveRange(t, f, 1, tip-1)
	ctx := f.Ctx.WithBlockHeight(tip)

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

	blockHashes := [][]byte{digest(1), digest(2), digest(3)}
	root := merkle.HashFromByteSlices(blockHashes)
	bundle := digest(9)
	require.NotEqual(t, bundle, root)

	const (
		start = int64(1)
		end   = int64(3)
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
	archiveRange(t, f, 1, 40)

	params, err := f.Query.Params(f.Ctx, &types.QueryParamsRequest{})
	require.NoError(t, err)
	require.Equal(t, types.DefaultBlocksIn14Days, params.Params.RetentionWindowBlocks)

	got, err := f.Query.Range(f.Ctx, &types.QueryRangeRequest{StartHeight: 1, EndHeight: 40})
	require.NoError(t, err)
	require.True(t, got.Range.Archived)

	last, err := f.Query.LastArchivedHeight(f.Ctx, &types.QueryLastArchivedHeightRequest{})
	require.NoError(t, err)
	require.Equal(t, int64(40), last.LastArchivedHeight)

	retain, err := f.Query.RetainHeight(f.Ctx, &types.QueryRetainHeightRequest{})
	require.NoError(t, err)
	require.Equal(t, int64(40), retain.LastArchivedHeight)
	require.Equal(t, f.Ctx.BlockHeight(), retain.Tip)
	require.LessOrEqual(t, retain.RetainHeight, retain.LastArchivedHeight)
}

func TestMsgSignerIsArchiver(t *testing.T) {
	signer := acc(7)
	attest := &types.MsgAttest{
		Archiver:    signer.String(),
		StartHeight: 1,
		EndHeight:   2,
		BundleCid:   "bafyarchivecid",
		BundleHash:  digest(1),
		MerkleRoot:  digest(2),
	}
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
	archiveRange(t, f, 1, 25)

	exported, err := f.Keeper.ExportGenesis(f.Ctx)
	require.NoError(t, err)
	require.NoError(t, exported.Validate())
	require.Equal(t, int64(25), exported.LastArchivedHeight)
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
	msg := &types.MsgAttest{
		Archiver: acc(1).String(), NodeId: nodeOf(2), StartHeight: 1, EndHeight: 50,
		BundleCid: "bafyarchivecid", BundleHash: digest(1), MerkleRoot: digest(2),
	}
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
	require.Equal(t, []string{opOf(1)}, rec.Operators)
	require.Equal(t, []string{acc(1).String()}, rec.Archivers)

	// The same operator's node with a wrong root is still refused.
	_, err = f.Msg.Attest(f.Ctx, &types.MsgAttest{
		Archiver: acc(2).String(), NodeId: nodeOf(2), StartHeight: 1, EndHeight: 50,
		BundleCid: "bafyarchivecid", BundleHash: digest(1), MerkleRoot: digest(9),
	})
	require.ErrorIs(t, err, types.ErrWrongRoot)
}

// A key that already attested is idempotent even after its node lost the role.
func TestAttest_repeatAfterLosingTheRoleIsIdempotent(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	f.attest(t, 1, 1, 50, "bafyarchivecid", digest(1), digest(2))
	f.Nodes.inactive[nodeOf(1)] = true
	res := f.attest(t, 1, 1, 50, "bafyarchivecid", digest(1), digest(2))
	require.Equal(t, uint32(1), res.Attesters)
}

func TestAttachReplicas_onlyActiveArchiveDeals(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	f.attest(t, 1, 1, 50, "bafyarchivecid", digest(1), digest(2))
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
	f.attest(t, 1, 51, 100, "bafyarchivecid", digest(1), digest(3))
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

	g.attest(t, 1, 51, 100, "bafyarchivecid", digest(1), digest(3))
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
	f.attest(t, 1, 1, 50, "bafyarchivecid", digest(1), digest(2))
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
	f.attest(t, 1, 1, 50, "bafyarchivecid", digest(1), digest(2))
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
	f.attest(t, 1, 1, 50, "bafyarchivecid", digest(1), digest(2))

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
	f.attest(t, 1, 1, 50, "bafyarchivecid", digest(1), digest(2))
	f.attest(t, 1, 51, 100, "bafyarchivecid", digest(1), digest(3))
	id := f.createDeal(t, 1, 1, 50)
	f.Storage.activate(id)
	_, err := f.Msg.AttachReplicas(f.Ctx, &types.MsgAttachReplicas{
		Archiver: acc(1).String(), NodeId: nodeOf(1), StartHeight: 51, EndHeight: 100, DealIds: []string{fmt.Sprint(id)},
	})
	require.ErrorIs(t, err, types.ErrDealAttached)
}
