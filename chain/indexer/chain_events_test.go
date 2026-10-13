package indexer

import (
	"context"
	"encoding/hex"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	abci "github.com/cometbft/cometbft/abci/types"

	cnfttypes "github.com/DeBrosOfficial/network/chain/x/cnft/types"
	markettypes "github.com/DeBrosOfficial/network/chain/x/market/types"
)

// withEvents adds events to a transaction's result, after the ones it has.
func withEvents(tx fakeTx, events ...abci.Event) fakeTx {
	tx.res.Events = append(tx.res.Events, events...)
	return tx
}

func u(n uint64) string { return strconv.FormatUint(n, 10) }

// settleEventOf is the market_settle event message msgIndex of a transaction carries.
func settleEventOf(msgIndex int, listing, bid, tree uint64, leaf uint32, asset []byte, buyer string) abci.Event {
	return abci.Event{Type: markettypes.EventTypeSettle, Attributes: attrs(
		markettypes.AttributeKeyListingID, u(listing), markettypes.AttributeKeyBidID, u(bid),
		markettypes.AttributeKeyTreeID, u(tree), markettypes.AttributeKeyLeafIndex, u(uint64(leaf)),
		markettypes.AttributeKeyAssetID, hex.EncodeToString(asset), markettypes.AttributeKeyBuyer, buyer,
		msgIndexKey, strconv.Itoa(msgIndex))}
}

// compressEventOf is the cnft_compress event message msgIndex of a transaction carries.
func compressEventOf(msgIndex int, asset []byte, delegate, cid string, creatorHash []byte, hashID uint32, tree uint64, leaf uint32) abci.Event {
	return abci.Event{Type: cnfttypes.EventTypeCompress, Attributes: attrs(
		cnfttypes.AttributeKeyAssetID, hex.EncodeToString(asset), cnfttypes.AttributeKeyDelegate, delegate,
		cnfttypes.AttributeKeyMetadataCID, cid, cnfttypes.AttributeKeyCreatorHash, hex.EncodeToString(creatorHash),
		cnfttypes.AttributeKeyHashID, u(uint64(hashID)), cnfttypes.AttributeKeyTreeID, u(tree),
		cnfttypes.AttributeKeyLeafIndex, u(uint64(leaf)), msgIndexKey, strconv.Itoa(msgIndex))}
}

func settleResponse() *markettypes.MsgSettleResponse {
	return &markettypes.MsgSettleResponse{Price: math.OneInt(), Royalty: math.ZeroInt(), SellerProceeds: math.OneInt()}
}

// prunedChain is a chain whose node has pruned every height below the one it serves: a read of the
// state before a block fails the way a real pruned node does, and the tree is the only thing it can
// answer (x/cnft never changes a tree's collection).
func prunedChain(t *testing.T, treeID, collection uint64, blocks int) *fakeChain {
	t.Helper()
	chain := newFakeChain()
	for range blocks {
		chain.add()
	}
	chain.prunedBelow = int64(blocks) + 1
	chain.queries[queryKey(queryTree, 0)] = &cnfttypes.QueryTreeResponse{Tree: cnfttypes.Tree{Id: treeID, CollectionId: collection}}
	return chain
}

func TestFollower_settleOfAListingFromBeforeTheStartNeedsNoPriorState(t *testing.T) {
	alice, bob, erin := addr(t, 1), addr(t, 2), addr(t, 5)
	id, id2 := assetID(3), assetID(4)
	leaf := cnfttypes.Leaf{AssetId: id, Owner: alice, MetadataCid: "bafy", CreatorHash: assetID(7), Nonce: 4, HashId: cnfttypes.HashIDSHA256}
	leaf2 := cnfttypes.Leaf{AssetId: id2, Owner: alice, MetadataCid: "bafy2", CreatorHash: assetID(7), HashId: cnfttypes.HashIDSHA256}
	chain := prunedChain(t, 5, 6, 3)
	chain.add(
		withEvents(okTx(t, one(&markettypes.MsgSettle{Signer: erin, ListingId: 11, Leaf: leaf, Proof: cnfttypes.MerkleProof{Index: 9}}),
			one(settleResponse()), erin), settleEventOf(0, 11, 0, 5, 9, id, erin)),
		withEvents(okTx(t, one(&markettypes.MsgSettle{Signer: alice, ListingId: 12, BidId: 4, Leaf: leaf2, Proof: cnfttypes.MerkleProof{Index: 2}}),
			one(settleResponse()), alice), settleEventOf(0, 12, 4, 5, 2, id2, bob)),
	)
	store := index(t, chain, 4)

	a := onlyAsset(t, store, id)
	require.Equal(t, erin, a.Owner, "a buy-now settle goes to the signer")
	require.Equal(t, uint64(5), a.Nonce)
	require.Equal(t, uint32(9), a.LeafIndex)
	require.Equal(t, uint64(6), a.CollectionID)
	require.Equal(t, bob, onlyAsset(t, store, id2).Owner, "an accepted bid goes to the bidder the event names")
	for _, q := range chain.asked {
		require.NotContains(t, q, queryListing)
		require.NotContains(t, q, queryBid)
	}
}

func TestFollower_compressOfAnAssetDecompressedBeforeTheStartNeedsNoPriorState(t *testing.T) {
	alice, dave := addr(t, 1), addr(t, 4)
	id := assetID(5)
	chain := prunedChain(t, 2, 1, 2)
	chain.add(withEvents(
		okTx(t, one(&cnfttypes.MsgCompress{Owner: alice, AssetId: id}), one(&cnfttypes.MsgCompressResponse{TreeId: 2, LeafIndex: 40, Nonce: 6}), alice),
		compressEventOf(0, id, dave, "bafyOld", assetID(7), cnfttypes.HashIDSHA256, 2, 40)))
	store := index(t, chain, 3)

	a := onlyAsset(t, store, id)
	require.Equal(t, "bafyOld", a.MetadataCID)
	require.Equal(t, dave, a.Delegate)
	require.Equal(t, hex.EncodeToString(assetID(7)), a.CreatorHash)
	require.Equal(t, uint32(40), a.LeafIndex)
	require.Equal(t, AssetCompressed, a.State)
	require.NotContains(t, chain.asked, queryKey(queryDecompressed, 2))
}

func TestFollower_compressDropsTheDecompressedRecordTheIndexHolds(t *testing.T) {
	alice := addr(t, 1)
	id := assetID(9)
	chain := newFakeChain()
	chain.queries[queryKey(queryTree, 0)] = &cnfttypes.QueryTreeResponse{Tree: cnfttypes.Tree{Id: 4, CollectionId: 8}}
	leaf := cnfttypes.Leaf{AssetId: id, Owner: alice, MetadataCid: "bafy", CreatorHash: assetID(7), HashId: cnfttypes.HashIDSHA256}
	chain.add(
		okTx(t, one(&cnfttypes.MsgDecompress{Owner: alice, TreeId: 4, Current: leaf, Proof: cnfttypes.MerkleProof{Index: 12}}), one(&cnfttypes.MsgDecompressResponse{}), alice),
		withEvents(okTx(t, one(&cnfttypes.MsgCompress{Owner: alice, AssetId: id}), one(&cnfttypes.MsgCompressResponse{TreeId: 4, LeafIndex: 13, Nonce: 1}), alice),
			compressEventOf(0, id, "", "bafy", assetID(7), cnfttypes.HashIDSHA256, 4, 13)),
	)
	store := index(t, chain, 1)
	a := onlyAsset(t, store, id)
	require.Equal(t, AssetCompressed, a.State, "the decompressed record moved to the new leaf, it is not kept beside it")
	require.Equal(t, uint32(13), a.LeafIndex)
}

func TestFollower_eventsAreMatchedToTheirMessageByIndex(t *testing.T) {
	alice, bob, erin := addr(t, 1), addr(t, 2), addr(t, 5)
	idA, idB := assetID(3), assetID(4)
	leafA := cnfttypes.Leaf{AssetId: idA, Owner: alice, MetadataCid: "a", CreatorHash: assetID(7), HashId: cnfttypes.HashIDSHA256}
	leafB := cnfttypes.Leaf{AssetId: idB, Owner: alice, MetadataCid: "b", CreatorHash: assetID(7), HashId: cnfttypes.HashIDSHA256}
	chain := prunedChain(t, 5, 6, 1)
	chain.add(withEvents(okTx(t, msgs{
		&markettypes.MsgSettle{Signer: erin, ListingId: 1, Leaf: leafA, Proof: cnfttypes.MerkleProof{Index: 1}},
		&markettypes.MsgSettle{Signer: bob, ListingId: 2, Leaf: leafB, Proof: cnfttypes.MerkleProof{Index: 2}},
	}, msgs{settleResponse(), settleResponse()}, erin, bob),
		settleEventOf(1, 2, 0, 5, 2, idB, bob), settleEventOf(0, 1, 0, 5, 1, idA, erin)))
	store := index(t, chain, 2)
	require.Equal(t, erin, onlyAsset(t, store, idA).Owner)
	require.Equal(t, bob, onlyAsset(t, store, idB).Owner)
}

func TestFollower_settleWithoutAnEventBeforeTheChainEmittedOneReadsThePriorState(t *testing.T) {
	erin := addr(t, 5)
	id := assetID(3)
	leaf := cnfttypes.Leaf{AssetId: id, Owner: addr(t, 1), MetadataCid: "bafy", CreatorHash: assetID(7), HashId: cnfttypes.HashIDSHA256}
	chain := newFakeChain()
	chain.add()
	chain.add(okTx(t, one(&markettypes.MsgSettle{Signer: erin, ListingId: 11, Leaf: leaf, Proof: cnfttypes.MerkleProof{Index: 9}}), one(settleResponse()), erin))
	chain.queries[queryKey(queryListing, 1)] = &markettypes.QueryListingResponse{Listing: markettypes.Listing{Id: 11, TreeId: 5}}
	chain.queries[queryKey(queryTree, 0)] = &cnfttypes.QueryTreeResponse{Tree: cnfttypes.Tree{Id: 5, CollectionId: 6}}
	store := index(t, chain, 2)
	require.Equal(t, erin, onlyAsset(t, store, id).Owner)
	require.Contains(t, chain.asked, queryKey(queryListing, 1))
	from, seen, err := (&writer{b: store.db.NewIndexedBatch()}).eventsFrom(keyEventsFrom)
	require.NoError(t, err)
	require.False(t, seen, "no event was seen, so no height was recorded")
	require.Zero(t, from)
}

// The chain moved to a binary that emits the events at height 3 (the first settle after the
// upgrade carries one); the settle at height 2 ran on the old binary and is read from the state.
func TestFollower_afterTheFirstEventASettleWithoutOneStopsTheFollower(t *testing.T) {
	alice, erin := addr(t, 1), addr(t, 5)
	idOld, idNew, idBad := assetID(3), assetID(4), assetID(5)
	mk := func(id []byte) cnfttypes.Leaf {
		return cnfttypes.Leaf{AssetId: id, Owner: alice, MetadataCid: "bafy", CreatorHash: assetID(7), HashId: cnfttypes.HashIDSHA256}
	}
	chain := newFakeChain()
	chain.add()
	chain.add(okTx(t, one(&markettypes.MsgSettle{Signer: erin, ListingId: 11, Leaf: mk(idOld), Proof: cnfttypes.MerkleProof{Index: 1}}), one(settleResponse()), erin))
	chain.add(withEvents(okTx(t, one(&markettypes.MsgSettle{Signer: erin, ListingId: 12, Leaf: mk(idNew), Proof: cnfttypes.MerkleProof{Index: 2}}), one(settleResponse()), erin),
		settleEventOf(0, 12, 0, 5, 2, idNew, erin)))
	chain.add(okTx(t, one(&markettypes.MsgSettle{Signer: erin, ListingId: 13, Leaf: mk(idBad), Proof: cnfttypes.MerkleProof{Index: 3}}), one(settleResponse()), erin))
	chain.queries[queryKey(queryListing, 1)] = &markettypes.QueryListingResponse{Listing: markettypes.Listing{Id: 11, TreeId: 5}}
	chain.queries[queryKey(queryTree, 0)] = &cnfttypes.QueryTreeResponse{Tree: cnfttypes.Tree{Id: 5, CollectionId: 6}}

	store := openStore(t, t.TempDir())
	defer store.Close()
	f, err := NewFollower(chain, store, 2)
	require.NoError(t, err)
	_, err = f.Step(context.Background())
	require.ErrorContains(t, err, "no market_settle event")
	require.ErrorContains(t, err, "since height 3")

	require.Equal(t, erin, onlyAsset(t, store, idOld).Owner, "the old binary's settle was indexed from the state")
	require.Equal(t, erin, onlyAsset(t, store, idNew).Owner)
	st, err := store.Status()
	require.NoError(t, err)
	require.Equal(t, int64(3), st.Cursor, "the block with the missing event was not indexed")
	for _, q := range chain.asked {
		require.NotContains(t, q, queryKey(queryListing, 3), "the state is not read to cover for a missing event")
	}
}

func TestFollower_aCompressWithoutAnEventAfterASettleEventStops(t *testing.T) {
	alice, erin := addr(t, 1), addr(t, 5)
	id := assetID(3)
	leaf := cnfttypes.Leaf{AssetId: id, Owner: alice, MetadataCid: "bafy", CreatorHash: assetID(7), HashId: cnfttypes.HashIDSHA256}
	chain := prunedChain(t, 5, 6, 1)
	chain.add(withEvents(okTx(t, one(&markettypes.MsgSettle{Signer: erin, ListingId: 11, Leaf: leaf, Proof: cnfttypes.MerkleProof{Index: 1}}), one(settleResponse()), erin),
		settleEventOf(0, 11, 0, 5, 1, id, erin)))
	chain.add(okTx(t, one(&cnfttypes.MsgCompress{Owner: alice, AssetId: assetID(8)}), one(&cnfttypes.MsgCompressResponse{TreeId: 5, LeafIndex: 2, Nonce: 1}), alice))
	store := openStore(t, t.TempDir())
	defer store.Close()
	f, err := NewFollower(chain, store, 2)
	require.NoError(t, err)
	_, err = f.Step(context.Background())
	require.ErrorContains(t, err, "no cnft_compress event")
}

func TestFollower_anEventThatIsNotTheMessagesStopsTheFollower(t *testing.T) {
	alice, bob, erin := addr(t, 1), addr(t, 2), addr(t, 5)
	id := assetID(3)
	leaf := cnfttypes.Leaf{AssetId: id, Owner: alice, MetadataCid: "bafy", CreatorHash: assetID(7), HashId: cnfttypes.HashIDSHA256}
	for name, ev := range map[string]abci.Event{
		"another listing":  settleEventOf(0, 99, 0, 5, 1, id, erin),
		"another asset":    settleEventOf(0, 11, 0, 5, 1, assetID(4), erin),
		"another leaf":     settleEventOf(0, 11, 0, 5, 2, id, erin),
		"another buyer":    settleEventOf(0, 11, 0, 5, 1, id, bob),
		"a malformed tree": {Type: markettypes.EventTypeSettle, Attributes: attrs(markettypes.AttributeKeyListingID, "11", markettypes.AttributeKeyBidID, "0", markettypes.AttributeKeyTreeID, "x", msgIndexKey, "0")},
		"a missing buyer":  {Type: markettypes.EventTypeSettle, Attributes: attrs(markettypes.AttributeKeyListingID, "11", markettypes.AttributeKeyBidID, "0", markettypes.AttributeKeyTreeID, "5", markettypes.AttributeKeyLeafIndex, "1", markettypes.AttributeKeyAssetID, hex.EncodeToString(id), msgIndexKey, "0")},
	} {
		t.Run(name, func(t *testing.T) {
			chain := prunedChain(t, 5, 6, 1)
			chain.add(withEvents(okTx(t, one(&markettypes.MsgSettle{Signer: erin, ListingId: 11, Leaf: leaf, Proof: cnfttypes.MerkleProof{Index: 1}}), one(settleResponse()), erin), ev))
			store := openStore(t, t.TempDir())
			defer store.Close()
			f, err := NewFollower(chain, store, 2)
			require.NoError(t, err)
			_, err = f.Step(context.Background())
			require.Error(t, err)
			st, err := store.Status()
			require.NoError(t, err)
			require.Zero(t, st.Cursor, "the block was not indexed")
		})
	}
}

func TestFollower_aCompressEventThatIsNotTheMessagesStopsTheFollower(t *testing.T) {
	alice := addr(t, 1)
	id := assetID(5)
	for name, ev := range map[string]abci.Event{
		"another asset": compressEventOf(0, assetID(6), "", "c", assetID(7), 1, 2, 40),
		"another tree":  compressEventOf(0, id, "", "c", assetID(7), 1, 3, 40),
		"another leaf":  compressEventOf(0, id, "", "c", assetID(7), 1, 2, 41),
		"bad hex":       {Type: cnfttypes.EventTypeCompress, Attributes: attrs(cnfttypes.AttributeKeyAssetID, "zz", msgIndexKey, "0")},
	} {
		t.Run(name, func(t *testing.T) {
			chain := prunedChain(t, 2, 1, 1)
			chain.add(withEvents(okTx(t, one(&cnfttypes.MsgCompress{Owner: alice, AssetId: id}), one(&cnfttypes.MsgCompressResponse{TreeId: 2, LeafIndex: 40, Nonce: 6}), alice), ev))
			store := openStore(t, t.TempDir())
			defer store.Close()
			f, err := NewFollower(chain, store, 2)
			require.NoError(t, err)
			_, err = f.Step(context.Background())
			require.Error(t, err)
		})
	}
}
