package keeper_test

import (
	"testing"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"

	"github.com/DeBrosOfficial/network/chain/x/cnft/keeper"
	"github.com/DeBrosOfficial/network/chain/x/cnft/types"
)

func TestCreateTree_locksDeposit(t *testing.T) {
	f := newFixture(t)
	creator := bech(1)
	collection := f.createCollection(t, creator, 500)
	const depth, buffer, canopy = 3, 2, 1
	id, _ := f.createTree(t, creator, collection, depth, buffer, canopy)

	want, err := types.TreeDeposit(depth, buffer, canopy)
	require.NoError(t, err)
	require.Len(t, f.fees.locks, 1)
	require.Equal(t, creator, f.fees.locks[0].owner.String())
	require.Equal(t, types.TreeDepositID(id), f.fees.locks[0].id)
	require.True(t, f.fees.locks[0].amount.Equal(want))
	require.True(t, want.Equal(math.NewInt(588)))
}

func TestTwoMintsInOneBlock_keepIntermediateRoot(t *testing.T) {
	f := newFixture(t)
	creator := bech(1)
	owner := bech(2)
	collection := f.createCollection(t, creator, 0)
	id, empty := f.createTree(t, creator, collection, 4, 2, 0)

	_, err := f.msg.Mint(f.ctx, mintMsg(creator, id, empty, owner, 1))
	require.NoError(t, err)
	tree, err := f.keeper.GetTree(f.ctx, id)
	require.NoError(t, err)
	intermediate := tree.Root()

	_, err = f.msg.Mint(f.ctx, mintMsg(creator, id, empty, owner, 2))
	require.NoError(t, err)
	tree, err = f.keeper.GetTree(f.ctx, id)
	require.NoError(t, err)
	require.True(t, tree.ContainsRoot(intermediate), "the buffer must keep the root produced between the two mints")
	require.False(t, tree.ContainsRoot(empty))
	require.Equal(t, uint32(2), tree.RightmostIndex())

	narrow := newFixture(t)
	collection = narrow.createCollection(t, creator, 0)
	id, empty = narrow.createTree(t, creator, collection, 4, 1, 0)
	_, err = narrow.msg.Mint(narrow.ctx, mintMsg(creator, id, empty, owner, 1))
	require.NoError(t, err)
	_, err = narrow.msg.Mint(narrow.ctx, mintMsg(creator, id, empty, owner, 2))
	require.ErrorIs(t, err, types.ErrRootNotInBuffer)
}

func TestMsgTransfer_doesNotCreditEarnings(t *testing.T) {
	f := newFixture(t)
	require.Equal(t, f.earnings, f.keeper.Earnings())
	creator := bech(1)
	seller := bech(2)
	buyer := bech(3)
	collection := f.createCollection(t, creator, 500)
	id, root := f.createTree(t, creator, collection, 4, 8, 0)
	body := leafBody(t, creator, seller, assetID(1), "cid-1", 0)
	res, err := f.msg.Mint(f.ctx, &types.MsgMint{
		Creator: creator, TreeId: id, Root: root,
		Leaves: []types.MintLeaf{{AssetId: body.AssetId, Owner: seller, MetadataCid: body.MetadataCid}},
	})
	require.NoError(t, err)
	proof := prove(t, [][]byte{mustHash(t, body)}, 0, 4)
	_, err = f.msg.Transfer(f.ctx, &types.MsgTransfer{
		Signer: seller, TreeId: id, Current: body, NewOwner: buyer, Proof: proof,
	})
	require.NoError(t, err)
	require.Zero(t, f.earnings.calls)

	buyerAddr, err := sdk.AccAddressFromBech32(buyer)
	require.NoError(t, err)
	moved := body
	moved.Owner = buyer
	moved.Nonce = 1
	_, err = f.keeper.ProveOwned(f.ctx, id, moved, prove(t, [][]byte{mustHash(t, moved)}, 0, 4), buyerAddr)
	require.NoError(t, err)
	require.Equal(t, res.LeafIndices, []uint32{0})
}

func TestStaleProofAndRootOlderThanBuffer(t *testing.T) {
	f := newFixture(t)
	creator := bech(1)
	owner := bech(2)
	other := bech(3)
	collection := f.createCollection(t, creator, 0)
	id, root := f.createTree(t, creator, collection, 4, 8, 0)
	first := leafBody(t, creator, owner, assetID(1), "cid-a", 0)
	second := leafBody(t, creator, owner, assetID(2), "cid-b", 0)
	_, err := f.msg.Mint(f.ctx, &types.MsgMint{
		Creator: creator, TreeId: id, Root: root,
		Leaves: []types.MintLeaf{
			{AssetId: first.AssetId, Owner: owner, MetadataCid: first.MetadataCid},
			{AssetId: second.AssetId, Owner: owner, MetadataCid: second.MetadataCid},
		},
	})
	require.NoError(t, err)
	leaves := [][]byte{mustHash(t, first), mustHash(t, second)}
	proof := prove(t, leaves, 0, 4)
	_, err = f.msg.Transfer(f.ctx, &types.MsgTransfer{
		Signer: owner, TreeId: id, Current: first, NewOwner: other, Proof: proof,
	})
	require.NoError(t, err)
	_, err = f.msg.Transfer(f.ctx, &types.MsgTransfer{
		Signer: owner, TreeId: id, Current: first, NewOwner: bech(4), Proof: proof,
	})
	require.ErrorIs(t, err, types.ErrStaleProof)

	old := newFixture(t)
	collection = old.createCollection(t, creator, 0)
	id, empty := old.createTree(t, creator, collection, 3, 2, 0)
	_, err = old.msg.Mint(old.ctx, mintMsg(creator, id, empty, owner, 1))
	require.NoError(t, err)
	tree, err := old.keeper.GetTree(old.ctx, id)
	require.NoError(t, err)
	_, err = old.msg.Mint(old.ctx, mintMsg(creator, id, tree.Root(), owner, 2))
	require.NoError(t, err)
	tree, err = old.keeper.GetTree(old.ctx, id)
	require.NoError(t, err)
	_, err = old.msg.Mint(old.ctx, mintMsg(creator, id, tree.Root(), owner, 3))
	require.NoError(t, err)
	staleBody := leafBody(t, creator, owner, assetID(1), "cid-1", 0)
	_, err = old.msg.Transfer(old.ctx, &types.MsgTransfer{
		Signer: owner, TreeId: id, Current: staleBody, NewOwner: other,
		Proof: prove(t, [][]byte{mustHash(t, staleBody)}, 0, 3),
	})
	require.ErrorIs(t, err, types.ErrRootNotInBuffer)
}

func TestDecompressCompressRoundTrip(t *testing.T) {
	f := newFixture(t)
	creator := bech(1)
	owner := bech(2)
	collection := f.createCollection(t, creator, 0)
	id, root := f.createTree(t, creator, collection, 4, 4, 0)
	body := leafBody(t, creator, owner, assetID(9), "cid-round", 0)
	_, err := f.msg.Mint(f.ctx, &types.MsgMint{
		Creator: creator, TreeId: id, Root: root,
		Leaves: []types.MintLeaf{{AssetId: body.AssetId, Owner: owner, MetadataCid: body.MetadataCid}},
	})
	require.NoError(t, err)
	_, err = f.msg.Decompress(f.ctx, &types.MsgDecompress{
		Owner: owner, TreeId: id, Current: body, Proof: prove(t, [][]byte{mustHash(t, body)}, 0, 4),
	})
	require.NoError(t, err)
	got, err := f.keeper.Decompressed.Get(f.ctx, body.AssetId)
	require.NoError(t, err)
	require.Equal(t, owner, got.Owner)
	require.Equal(t, body.MetadataCid, got.MetadataCid)

	tree, err := f.keeper.GetTree(f.ctx, id)
	require.NoError(t, err)
	res, err := f.msg.Compress(f.ctx, &types.MsgCompress{Owner: owner, AssetId: body.AssetId, Root: tree.Root()})
	require.NoError(t, err)
	require.Equal(t, uint32(1), res.LeafIndex)
	require.Equal(t, uint64(1), res.Nonce)
	_, err = f.keeper.Decompressed.Get(f.ctx, body.AssetId)
	require.Error(t, err)

	again := body
	again.Nonce = 1
	ownerAddr, err := sdk.AccAddressFromBech32(owner)
	require.NoError(t, err)
	_, err = f.keeper.ProveOwned(f.ctx, id, again, prove(t, [][]byte{types.ZeroLeaf(), mustHash(t, again)}, 1, 4), ownerAddr)
	require.NoError(t, err)
}

func TestRebuildTreeFromTxBodies(t *testing.T) {
	creator := bech(1)
	owner := bech(2)
	buyer := bech(3)
	type tagged struct {
		kind byte
		msg  interface {
			proto.Message
			Marshal() ([]byte, error)
		}
	}
	steps := []tagged{
		{1, &types.MsgCreateCollection{Creator: creator, Name: "demo", RoyaltyBps: 250}},
		{2, &types.MsgCreateTree{Creator: creator, CollectionId: 1, Depth: 4, Buffer: 8, Canopy: 0}},
		{3, &types.MsgMint{Creator: creator, TreeId: 1, Leaves: []types.MintLeaf{
			{AssetId: assetID(1), Owner: owner, MetadataCid: "cid-1"},
			{AssetId: assetID(2), Owner: owner, MetadataCid: "cid-2"},
		}}},
		{4, &types.MsgTransfer{Signer: owner, TreeId: 1, Current: leafBody(t, creator, owner, assetID(1), "cid-1", 0), NewOwner: buyer}},
		{5, &types.MsgUpdateMetadata{Signer: owner, TreeId: 1, Current: leafBody(t, creator, owner, assetID(2), "cid-2", 0), NewMetadataCid: "cid-2b"}},
		{6, &types.MsgBurn{Signer: buyer, TreeId: 1, Current: leafBody(t, creator, buyer, assetID(1), "cid-1", 1)}},
		{3, &types.MsgMint{Creator: creator, TreeId: 1, Leaves: []types.MintLeaf{
			{AssetId: assetID(3), Owner: owner, MetadataCid: "cid-3"},
		}}},
	}

	f := newFixture(t)
	var bodies [][]byte
	var leaves [][]byte
	for _, step := range steps {
		fillProof(t, f, step.msg, leaves)
		bz, err := step.msg.Marshal()
		require.NoError(t, err)
		bodies = append(bodies, append([]byte{step.kind}, bz...))
		_, leaves = applyBody(t, f, bodies[len(bodies)-1], leaves)
	}
	tree, err := f.keeper.GetTree(f.ctx, 1)
	require.NoError(t, err)

	replay := newFixture(t)
	var replayLeaves [][]byte
	var root []byte
	for _, bz := range bodies {
		root, replayLeaves = applyBody(t, replay, bz, replayLeaves)
	}
	require.Equal(t, tree.Root(), root)
	require.Equal(t, leaves, replayLeaves)
}

func fillProof(t *testing.T, f *fixture, msg interface{}, leaves [][]byte) {
	t.Helper()
	switch m := msg.(type) {
	case *types.MsgMint:
		if len(m.Root) == 0 {
			tree, err := f.keeper.GetTree(f.ctx, m.TreeId)
			require.NoError(t, err)
			m.Root = tree.Root()
		}
	case *types.MsgTransfer:
		m.Proof = prove(t, leaves, 0, 4)
	case *types.MsgUpdateMetadata:
		m.Proof = prove(t, leaves, 1, 4)
	case *types.MsgBurn:
		m.Proof = prove(t, leaves, 0, 4)
	}
}

func applyBody(t *testing.T, f *fixture, body []byte, leaves [][]byte) ([]byte, [][]byte) {
	t.Helper()
	require.NotEmpty(t, body)
	bz := body[1:]
	switch body[0] {
	case 1:
		var msg types.MsgCreateCollection
		require.NoError(t, msg.Unmarshal(bz))
		_, err := f.msg.CreateCollection(f.ctx, &msg)
		require.NoError(t, err)
		return nil, leaves
	case 2:
		var msg types.MsgCreateTree
		require.NoError(t, msg.Unmarshal(bz))
		res, err := f.msg.CreateTree(f.ctx, &msg)
		require.NoError(t, err)
		return res.Root, leaves
	case 3:
		var msg types.MsgMint
		require.NoError(t, msg.Unmarshal(bz))
		res, err := f.msg.Mint(f.ctx, &msg)
		require.NoError(t, err)
		for i, in := range msg.Leaves {
			body := leafBody(t, bech(1), in.Owner, in.AssetId, in.MetadataCid, 0)
			require.Equal(t, res.LeafIndices[i], uint32(len(leaves)))
			leaves = append(leaves, mustHash(t, body))
		}
		return res.Root, leaves
	case 4:
		var msg types.MsgTransfer
		require.NoError(t, msg.Unmarshal(bz))
		res, err := f.msg.Transfer(f.ctx, &msg)
		require.NoError(t, err)
		next := msg.Current
		next.Owner = msg.NewOwner
		next.Delegate = msg.NewDelegate
		next.Nonce = res.Nonce
		leaves[msg.Proof.Index] = mustHash(t, next)
		return res.Root, leaves
	case 5:
		var msg types.MsgUpdateMetadata
		require.NoError(t, msg.Unmarshal(bz))
		res, err := f.msg.UpdateMetadata(f.ctx, &msg)
		require.NoError(t, err)
		next := msg.Current
		next.MetadataCid = msg.NewMetadataCid
		next.Nonce = res.Nonce
		leaves[msg.Proof.Index] = mustHash(t, next)
		return res.Root, leaves
	case 6:
		var msg types.MsgBurn
		require.NoError(t, msg.Unmarshal(bz))
		res, err := f.msg.Burn(f.ctx, &msg)
		require.NoError(t, err)
		leaves[msg.Proof.Index] = types.ZeroLeaf()
		return res.Root, leaves
	default:
		t.Fatalf("unrecognized tx body kind %d", body[0])
		return nil, nil
	}
}

func TestRecordSnapshot_storesCIDWithoutStorage(t *testing.T) {
	f := newFixture(t)
	creator := bech(1)
	collection := f.createCollection(t, creator, 0)
	id, _ := f.createTree(t, creator, collection, 3, 2, 0)
	res, err := f.msg.RecordSnapshot(f.ctx, &types.MsgRecordSnapshot{Creator: creator, TreeId: id, Cid: "bafybeigdyrzt5snapshot"})
	require.NoError(t, err)
	require.Equal(t, uint64(1), res.Id)
	resQ, err := keeper.NewQueryServerImpl(f.keeper).Snapshots(f.ctx, &types.QuerySnapshotsRequest{TreeId: id})
	require.NoError(t, err)
	require.Equal(t, []string{"bafybeigdyrzt5snapshot"}, []string{resQ.Snapshots[0].Cid})
	_, err = f.msg.RecordSnapshot(f.ctx, &types.MsgRecordSnapshot{Creator: bech(9), TreeId: id, Cid: "bafyrejected"})
	require.Error(t, err)
}

func mintMsg(creator string, treeID uint64, root []byte, owner string, n byte) *types.MsgMint {
	return &types.MsgMint{
		Creator: creator,
		TreeId:  treeID,
		Root:    root,
		Leaves: []types.MintLeaf{{
			AssetId:     assetID(n),
			Owner:       owner,
			MetadataCid: "cid-" + string(rune('0'+n)),
		}},
	}
}

func leafBody(t *testing.T, creator, owner string, asset []byte, cid string, nonce uint64) types.Leaf {
	t.Helper()
	addr, err := sdk.AccAddressFromBech32(creator)
	require.NoError(t, err)
	return types.Leaf{
		AssetId:     asset,
		Owner:       owner,
		MetadataCid: cid,
		CreatorHash: types.CreatorHash(addr),
		Nonce:       nonce,
		HashId:      types.HashIDSHA256,
	}
}

func mustHash(t *testing.T, leaf types.Leaf) []byte {
	t.Helper()
	hash, err := types.HashLeaf(leaf)
	require.NoError(t, err)
	return hash
}

func prove(t *testing.T, leaves [][]byte, index int, depth uint32) types.MerkleProof {
	t.Helper()
	siblings, root, err := types.Proof(leaves, index, depth)
	require.NoError(t, err)
	return types.MerkleProof{Root: root, Index: uint32(index), Siblings: siblings}
}

// A public query must not walk a whole tree's history: Snapshots returns at most
// MaxSnapshotsPerQuery entries, oldest first.
func TestSnapshotsQuery_isBounded(t *testing.T) {
	f := newFixture(t)
	for id := uint64(1); id <= keeper.MaxSnapshotsPerQuery+5; id++ {
		require.NoError(t, f.keeper.Snapshots.Set(f.ctx, collections.Join(uint64(1), id), types.Snapshot{TreeId: 1, Id: id, Cid: "c", Sequence: id}))
	}
	res, err := keeper.NewQueryServerImpl(f.keeper).Snapshots(f.ctx, &types.QuerySnapshotsRequest{TreeId: 1})
	require.NoError(t, err)
	require.Len(t, res.Snapshots, keeper.MaxSnapshotsPerQuery)
	require.Equal(t, uint64(1), res.Snapshots[0].Id)

	res, err = keeper.NewQueryServerImpl(f.keeper).Snapshots(f.ctx, &types.QuerySnapshotsRequest{TreeId: 2})
	require.NoError(t, err)
	require.Empty(t, res.Snapshots)
}
