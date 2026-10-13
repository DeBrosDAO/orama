package keeper_test

import (
	"encoding/hex"
	"testing"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	cnfttypes "github.com/DeBrosOfficial/network/chain/x/cnft/types"
	"github.com/DeBrosOfficial/network/chain/x/market/types"
)

func settleEvents(ctx sdk.Context) []map[string]string {
	var out []map[string]string
	for _, e := range ctx.EventManager().Events() {
		if e.Type != types.EventTypeSettle {
			continue
		}
		attrs := map[string]string{}
		for _, a := range e.Attributes {
			attrs[a.Key] = a.Value
		}
		out = append(out, attrs)
	}
	return out
}

// listed mints one leaf for seller in a new tree and lists it at price, returning what a settle needs.
func listed(t *testing.T, f *fixture, seller string, price int64) (listing uint64, body cnfttypes.Leaf, proof cnfttypes.MerkleProof) {
	t.Helper()
	creator := bech(1)
	collection, err := f.cnftMsg.CreateCollection(f.ctx, &cnfttypes.MsgCreateCollection{Creator: creator, Name: "paintings", RoyaltyBps: 500})
	require.NoError(t, err)
	created, err := f.cnftMsg.CreateTree(f.ctx, &cnfttypes.MsgCreateTree{Creator: creator, CollectionId: collection.Id, Depth: 4, Buffer: 4})
	require.NoError(t, err)
	body = leaf(t, creator, seller, assetID(6), "cid-event", 0)
	_, err = f.cnftMsg.Mint(f.ctx, &cnfttypes.MsgMint{
		Creator: creator, TreeId: created.Id, Root: created.Root,
		Leaves: []cnfttypes.MintLeaf{{AssetId: body.AssetId, Owner: seller, MetadataCid: body.MetadataCid}},
	})
	require.NoError(t, err)
	proof = prove(t, [][]byte{mustHash(t, body)}, 0, 4)
	res, err := f.msg.List(f.ctx, &types.MsgList{Seller: seller, TreeId: created.Id, Leaf: body, Proof: proof, Price: math.NewInt(price)})
	require.NoError(t, err)
	return res.Id, body, proof
}

func TestSettle_buyNowEmitsTheListingAndTheSigner(t *testing.T) {
	f := newFixture(t)
	seller, buyer := bech(2), bech(3)
	listing, body, proof := listed(t, f, seller, 10000)
	f.bank.add(userKey(mustAddr(t, buyer)), math.NewInt(10000))

	ctx := f.ctx.WithEventManager(sdk.NewEventManager())
	_, err := f.msg.Settle(ctx, &types.MsgSettle{Signer: buyer, ListingId: listing, Leaf: body, Proof: proof})
	require.NoError(t, err)

	got := settleEvents(ctx)
	require.Len(t, got, 1)
	require.Equal(t, map[string]string{
		types.AttributeKeyListingID: "1",
		types.AttributeKeyBidID:     "0",
		types.AttributeKeyTreeID:    "1",
		types.AttributeKeyLeafIndex: "0",
		types.AttributeKeyAssetID:   hex.EncodeToString(body.AssetId),
		types.AttributeKeyBuyer:     buyer,
	}, got[0])
}

func TestSettle_acceptedBidEmitsTheBidderNotTheSigner(t *testing.T) {
	f := newFixture(t)
	seller, bidder := bech(2), bech(4)
	listing, body, proof := listed(t, f, seller, 10000)
	f.bank.add(userKey(mustAddr(t, bidder)), math.NewInt(2000))
	bid, err := f.msg.Bid(f.ctx, &types.MsgBid{Bidder: bidder, ListingId: listing, Amount: math.NewInt(2000)})
	require.NoError(t, err)

	ctx := f.ctx.WithEventManager(sdk.NewEventManager())
	_, err = f.msg.Settle(ctx, &types.MsgSettle{Signer: seller, ListingId: listing, BidId: bid.Id, Leaf: body, Proof: proof})
	require.NoError(t, err)

	got := settleEvents(ctx)
	require.Len(t, got, 1)
	require.Equal(t, bidder, got[0][types.AttributeKeyBuyer], "a sale to a bid goes to the bidder, whom the message does not name")
	require.Equal(t, "1", got[0][types.AttributeKeyBidID])
}

func TestSettle_failedSettleEmitsNothing(t *testing.T) {
	f := newFixture(t)
	seller, buyer := bech(2), bech(3)
	listing, body, proof := listed(t, f, seller, 10000)

	ctx := f.ctx.WithEventManager(sdk.NewEventManager())
	_, err := f.msg.Settle(ctx, &types.MsgSettle{Signer: buyer, ListingId: listing, Leaf: body, Proof: proof})
	require.Error(t, err, "the buyer has no funds")
	_, err = f.msg.Settle(ctx, &types.MsgSettle{Signer: seller, ListingId: listing, Leaf: body, Proof: proof})
	require.Error(t, err, "the seller cannot buy their own listing")
	_, err = f.msg.Settle(ctx, &types.MsgSettle{Signer: buyer, ListingId: 99, Leaf: body, Proof: proof})
	require.Error(t, err, "no such listing")
	require.Empty(t, settleEvents(ctx))
}
