package indexer

import (
	"context"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	gogoproto "github.com/cosmos/gogoproto/proto"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/bech32"

	"github.com/DeBrosOfficial/network/chain/app/params"
	cnfttypes "github.com/DeBrosOfficial/network/chain/x/cnft/types"
	markettypes "github.com/DeBrosOfficial/network/chain/x/market/types"
)

type msgs = []gogoproto.Message

func one(m gogoproto.Message) msgs { return msgs{m} }

// chainLeafHash is the chain's own HashLeaf, which reads the SDK's global
// bech32 prefix; the indexer must agree with it.
func chainLeafHash(t *testing.T, leaf cnfttypes.Leaf) string {
	t.Helper()
	sdk.GetConfig().SetBech32PrefixForAccount(params.Bech32Prefix, params.Bech32PrefixAccPub)
	h, err := cnfttypes.HashLeaf(leaf)
	require.NoError(t, err)
	return hex.EncodeToString(h)
}

func index(t *testing.T, chain *fakeChain, start int64) *Store {
	t.Helper()
	store := openStore(t, t.TempDir())
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	f, err := NewFollower(chain, store, start)
	require.NoError(t, err)
	_, err = f.Step(context.Background())
	require.NoError(t, err)
	return store
}

func onlyAsset(t *testing.T, store *Store, id []byte) Asset {
	t.Helper()
	recs, err := store.Assets(id)
	require.NoError(t, err)
	require.Len(t, recs, 1)
	return recs[0]
}

func owned(t *testing.T, store *Store, owner string) []string {
	t.Helper()
	assets, err := store.OwnerAssets(owner, 1, MaxLimit)
	require.NoError(t, err)
	out := []string{}
	for _, a := range assets {
		out = append(out, a.ID)
	}
	return out
}

func TestFollower_cnftLifecycle(t *testing.T) {
	alice, bob, carol := addr(t, 1), addr(t, 2), addr(t, 3)
	idA, idB := assetID(1), assetID(2)
	chain := newFakeChain()

	chain.add(okTx(t, one(&cnfttypes.MsgCreateTree{Creator: alice, CollectionId: 3, Depth: 14, Buffer: 8}),
		one(&cnfttypes.MsgCreateTreeResponse{Id: 1}), alice))
	mint := &cnfttypes.MsgMint{Creator: alice, TreeId: 1, Leaves: []cnfttypes.MintLeaf{
		{AssetId: idA, Owner: alice, MetadataCid: "bafyA"},
		{AssetId: idB, Owner: alice, Delegate: bob, MetadataCid: "bafyB"},
	}}
	leafA := cnfttypes.Leaf{AssetId: idA, Owner: alice, MetadataCid: "bafyA", HashId: cnfttypes.HashIDSHA256}
	chain.add(
		okTx(t, one(mint), one(&cnfttypes.MsgMintResponse{LeafIndices: []uint32{0, 1}}), alice),
		failedTx(t, one(&cnfttypes.MsgTransfer{Signer: alice, TreeId: 1, Current: leafA, NewOwner: bob}), alice),
	)
	store := index(t, chain, 1)
	a := onlyAsset(t, store, idA)
	creatorHash := mustHex(t, a.CreatorHash)
	_, aliceBytes, err := bech32.DecodeAndConvert(alice)
	require.NoError(t, err)
	require.Equal(t, cnfttypes.CreatorHash(aliceBytes), creatorHash, "creator_hash is the chain's hash of the mint signer")
	leafA.CreatorHash = creatorHash
	require.Equal(t, AssetCompressed, a.State)
	require.Equal(t, uint64(3), a.CollectionID)
	require.Equal(t, alice, a.Owner, "the failed transfer changed nothing")
	require.Equal(t, chainLeafHash(t, leafA), a.LeafHash)
	require.ElementsMatch(t, []string{hex.EncodeToString(idA), hex.EncodeToString(idB)}, owned(t, store, alice))
	require.Empty(t, chain.askedBeyondAggregates(), "a tree created after the start height needs no query")

	leafB := cnfttypes.Leaf{AssetId: idB, Owner: alice, Delegate: bob, MetadataCid: "bafyB", CreatorHash: creatorHash, HashId: cnfttypes.HashIDSHA256}
	chain.add(
		okTx(t, one(&cnfttypes.MsgTransfer{Signer: alice, TreeId: 1, Current: leafA, NewOwner: carol, Proof: cnfttypes.MerkleProof{Index: 0}}),
			one(&cnfttypes.MsgTransferResponse{Nonce: 1}), alice, carol),
		okTx(t, one(&cnfttypes.MsgUpdateMetadata{Signer: bob, TreeId: 1, Current: leafB, NewMetadataCid: "bafyB2", Proof: cnfttypes.MerkleProof{Index: 1}}),
			one(&cnfttypes.MsgUpdateMetadataResponse{Nonce: 1}), bob),
	)
	leafA.Owner, leafA.Nonce = carol, 1
	chain.add(okTx(t, one(&cnfttypes.MsgDecompress{Owner: carol, TreeId: 1, Current: leafA, Proof: cnfttypes.MerkleProof{Index: 0}}),
		one(&cnfttypes.MsgDecompressResponse{}), carol))
	leafB.MetadataCid, leafB.Nonce = "bafyB2", 1
	chain.add(
		okTx(t, one(&cnfttypes.MsgCompress{Owner: carol, AssetId: idA}), one(&cnfttypes.MsgCompressResponse{TreeId: 1, LeafIndex: 2, Nonce: 2}), carol),
		okTx(t, one(&cnfttypes.MsgBurn{Signer: alice, TreeId: 1, Current: leafB, Proof: cnfttypes.MerkleProof{Index: 1}}), one(&cnfttypes.MsgBurnResponse{}), alice),
	)
	f, err := NewFollower(chain, store, 1)
	require.NoError(t, err)
	_, err = f.Step(context.Background())
	require.NoError(t, err)

	a = onlyAsset(t, store, idA)
	require.Equal(t, AssetCompressed, a.State)
	require.Equal(t, uint32(2), a.LeafIndex, "compress appended a new leaf and the old location is gone")
	require.Equal(t, carol, a.Owner)
	require.Equal(t, uint64(2), a.Nonce)
	require.Equal(t, int64(5), a.UpdatedHeight)
	leafA.Nonce = 2
	require.Equal(t, chainLeafHash(t, leafA), a.LeafHash)

	b := onlyAsset(t, store, idB)
	require.Equal(t, AssetBurned, b.State)
	require.Equal(t, "bafyB2", b.MetadataCID)
	require.Empty(t, b.LeafHash)

	require.Equal(t, []string{hex.EncodeToString(idA)}, owned(t, store, carol))
	require.Empty(t, owned(t, store, alice))
	require.Empty(t, owned(t, store, bob), "a delegate does not own the asset")
}

func TestFollower_decompressedAssetIsStillOwned(t *testing.T) {
	alice := addr(t, 1)
	id := assetID(9)
	chain := newFakeChain()
	chain.queries[queryKey(queryTree, 0)] = &cnfttypes.QueryTreeResponse{Tree: cnfttypes.Tree{Id: 4, CollectionId: 8}}
	leaf := cnfttypes.Leaf{AssetId: id, Owner: alice, MetadataCid: "bafy", CreatorHash: assetID(7), HashId: cnfttypes.HashIDSHA256}
	chain.add(okTx(t, one(&cnfttypes.MsgDecompress{Owner: alice, TreeId: 4, Current: leaf, Proof: cnfttypes.MerkleProof{Index: 12}}),
		one(&cnfttypes.MsgDecompressResponse{}), alice))
	store := index(t, chain, 1)
	a := onlyAsset(t, store, id)
	require.Equal(t, AssetDecompressed, a.State)
	require.Equal(t, uint64(8), a.CollectionID, "a tree from before the start height is read from the chain")
	require.Empty(t, a.LeafHash)
	require.Equal(t, []string{hex.EncodeToString(id)}, owned(t, store, alice))
}

func TestFollower_compressOfAnAssetDecompressedBeforeTheStart(t *testing.T) {
	alice := addr(t, 1)
	id := assetID(5)
	chain := newFakeChain()
	chain.add()
	chain.add()
	chain.add(okTx(t, one(&cnfttypes.MsgCompress{Owner: alice, AssetId: id}), one(&cnfttypes.MsgCompressResponse{TreeId: 2, LeafIndex: 40, Nonce: 6}), alice))
	chain.queries[queryKey(queryDecompressed, 2)] = &cnfttypes.QueryDecompressedResponse{Asset: cnfttypes.DecompressedAsset{
		AssetId: id, TreeId: 2, LeafIndex: 3, Owner: alice, MetadataCid: "bafyOld", CreatorHash: assetID(7), Nonce: 5, HashId: cnfttypes.HashIDSHA256,
	}}
	chain.queries[queryKey(queryTree, 0)] = &cnfttypes.QueryTreeResponse{Tree: cnfttypes.Tree{Id: 2, CollectionId: 1}}
	store := index(t, chain, 3)
	a := onlyAsset(t, store, id)
	require.Equal(t, "bafyOld", a.MetadataCID)
	require.Equal(t, uint32(40), a.LeafIndex)
	require.Contains(t, chain.asked, queryKey(queryDecompressed, 2), "read at the height before the block")
}

func TestFollower_compressFailsLoudlyWhenThePriorStateIsGone(t *testing.T) {
	alice := addr(t, 1)
	chain := newFakeChain()
	chain.add()
	chain.add(okTx(t, one(&cnfttypes.MsgCompress{Owner: alice, AssetId: assetID(5)}), one(&cnfttypes.MsgCompressResponse{TreeId: 2, LeafIndex: 1, Nonce: 1}), alice))
	store := openStore(t, t.TempDir())
	defer store.Close()
	f, err := NewFollower(chain, store, 2)
	require.NoError(t, err)
	_, err = f.Step(context.Background())
	require.ErrorContains(t, err, "read decompressed asset")
	st, err := store.Status()
	require.NoError(t, err)
	require.Zero(t, st.Cursor)
}

func TestFollower_marketSettleMovesTheLeafToTheBidder(t *testing.T) {
	alice, bob, dave := addr(t, 1), addr(t, 2), addr(t, 4)
	id := assetID(1)
	chain := newFakeChain()
	chain.add(
		okTx(t, one(&cnfttypes.MsgCreateTree{Creator: alice, CollectionId: 3}), one(&cnfttypes.MsgCreateTreeResponse{Id: 1}), alice),
		okTx(t, one(&cnfttypes.MsgMint{Creator: alice, TreeId: 1, Leaves: []cnfttypes.MintLeaf{{AssetId: id, Owner: alice, Delegate: dave, MetadataCid: "bafy"}}}),
			one(&cnfttypes.MsgMintResponse{LeafIndices: []uint32{0}}), alice),
	)
	store := index(t, chain, 1)
	leaf := cnfttypes.Leaf{AssetId: id, Owner: alice, Delegate: dave, MetadataCid: "bafy", CreatorHash: mustHex(t, onlyAsset(t, store, id).CreatorHash), HashId: cnfttypes.HashIDSHA256}
	price := math.NewInt(100)
	chain.add(
		okTx(t, one(&markettypes.MsgList{Seller: alice, TreeId: 1, Leaf: leaf, Price: price}), one(&markettypes.MsgListResponse{Id: 7}), alice),
		okTx(t, one(&markettypes.MsgBid{Bidder: bob, ListingId: 7, Amount: price}), one(&markettypes.MsgBidResponse{Id: 2}), bob),
		okTx(t, one(&markettypes.MsgBid{Bidder: dave, ListingId: 7, Amount: price}), one(&markettypes.MsgBidResponse{Id: 3}), dave),
		okTx(t, one(&markettypes.MsgSettle{Signer: alice, ListingId: 7, BidId: 2, Leaf: leaf}),
			one(&markettypes.MsgSettleResponse{Price: price, Royalty: math.ZeroInt(), SellerProceeds: price}), alice, bob),
	)
	f, err := NewFollower(chain, store, 1)
	require.NoError(t, err)
	_, err = f.Step(context.Background())
	require.NoError(t, err)

	a := onlyAsset(t, store, id)
	require.Equal(t, bob, a.Owner)
	require.Empty(t, a.Delegate, "a sale clears the delegate")
	require.Equal(t, uint64(1), a.Nonce)
	next := leaf
	next.Owner, next.Delegate, next.Nonce = bob, "", 1
	require.Equal(t, chainLeafHash(t, next), a.LeafHash)
	require.Empty(t, chain.askedBeyondAggregates(), "list and bids were in the index")
	_, ok, err := getRaw(store.db, listingKey(7))
	require.NoError(t, err)
	require.False(t, ok, "a settled listing is dropped")
	_, ok, err = getRaw(store.db, bidKey(7, 3))
	require.NoError(t, err)
	require.False(t, ok, "and the refunded bids with it")
}

func TestFollower_settleOfAListingFromBeforeTheStart(t *testing.T) {
	alice, bob, erin := addr(t, 1), addr(t, 2), addr(t, 5)
	id := assetID(3)
	leaf := cnfttypes.Leaf{AssetId: id, Owner: alice, MetadataCid: "bafy", CreatorHash: assetID(7), Nonce: 4, HashId: cnfttypes.HashIDSHA256}
	chain := newFakeChain()
	for range 3 {
		chain.add()
	}
	chain.add(
		okTx(t, one(&markettypes.MsgSettle{Signer: erin, ListingId: 11, Leaf: leaf, Proof: cnfttypes.MerkleProof{Index: 9}}),
			one(&markettypes.MsgSettleResponse{Price: math.OneInt(), Royalty: math.ZeroInt(), SellerProceeds: math.OneInt()}), erin),
		okTx(t, one(&markettypes.MsgSettle{Signer: alice, ListingId: 12, BidId: 4, Leaf: cnfttypes.Leaf{AssetId: assetID(4), Owner: alice, CreatorHash: assetID(7), HashId: 1}}),
			one(&markettypes.MsgSettleResponse{Price: math.OneInt(), Royalty: math.ZeroInt(), SellerProceeds: math.OneInt()}), alice),
	)
	chain.queries[queryKey(queryListing, 3)] = &markettypes.QueryListingResponse{Listing: markettypes.Listing{Id: 11, TreeId: 5}}
	chain.queries[queryKey(queryTree, 0)] = &cnfttypes.QueryTreeResponse{Tree: cnfttypes.Tree{Id: 5, CollectionId: 6}}
	chain.queries[queryKey(queryBid, 3)] = &markettypes.QueryBidResponse{Bid: markettypes.Bid{Id: 4, ListingId: 12, Bidder: bob}}
	store := index(t, chain, 4)

	a := onlyAsset(t, store, id)
	require.Equal(t, erin, a.Owner, "a buy-now settle goes to the signer")
	require.Equal(t, uint64(5), a.Nonce)
	require.Equal(t, uint32(9), a.LeafIndex)
	require.Equal(t, uint64(6), a.CollectionID)
	require.Equal(t, bob, onlyAsset(t, store, assetID(4)).Owner, "an accepted bid goes to the bidder")
}

func TestFollower_nonCanonicalAddressesAreStoredCanonically(t *testing.T) {
	alice, carol := addr(t, 1), addr(t, 3)
	long, err := bech32.ConvertAndEncode(params.Bech32Prefix, make([]byte, 64))
	require.NoError(t, err)
	id, idLong := assetID(1), assetID(2)
	chain := newFakeChain()
	chain.add(
		okTx(t, one(&cnfttypes.MsgCreateTree{Creator: alice, CollectionId: 3}), one(&cnfttypes.MsgCreateTreeResponse{Id: 1}), alice),
		okTx(t, one(&cnfttypes.MsgMint{Creator: strings.ToUpper(alice), TreeId: 1, Leaves: []cnfttypes.MintLeaf{
			{AssetId: id, Owner: alice, MetadataCid: "bafy"},
			{AssetId: idLong, Owner: long, MetadataCid: "bafy"},
		}}), one(&cnfttypes.MsgMintResponse{LeafIndices: []uint32{0, 1}}), alice),
	)
	store := index(t, chain, 1)
	leaf := cnfttypes.Leaf{AssetId: id, Owner: alice, MetadataCid: "bafy", CreatorHash: mustHex(t, onlyAsset(t, store, id).CreatorHash), HashId: cnfttypes.HashIDSHA256}
	require.Equal(t, long, onlyAsset(t, store, idLong).Owner, "a 64-byte address the chain accepts is indexed")

	chain.add(okTx(t, one(&cnfttypes.MsgTransfer{Signer: alice, TreeId: 1, Current: leaf, NewOwner: strings.ToUpper(carol)}),
		one(&cnfttypes.MsgTransferResponse{Nonce: 1}), alice))
	f, err := NewFollower(chain, store, 1)
	require.NoError(t, err)
	_, err = f.Step(context.Background())
	require.NoError(t, err, "an uppercase owner the chain accepts must not stop the indexer")

	a := onlyAsset(t, store, id)
	require.Equal(t, carol, a.Owner)
	next := leaf
	next.Owner, next.Nonce = carol, 1
	require.Equal(t, chainLeafHash(t, next), a.LeafHash)
	require.Equal(t, []string{hex.EncodeToString(id)}, owned(t, store, carol))
}
