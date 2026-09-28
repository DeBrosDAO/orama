package keeper_test

import (
	"testing"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"

	cnfttypes "github.com/DeBrosOfficial/network/chain/x/cnft/types"
	"github.com/DeBrosOfficial/network/chain/x/market/types"
)

func TestSplitSale_500BpsOf10000(t *testing.T) {
	creator, seller, err := types.SplitSale(math.NewInt(10000), 500)
	require.NoError(t, err)
	require.True(t, creator.Equal(math.NewInt(500)))
	require.True(t, seller.Equal(math.NewInt(9500)))
}

func TestSettle_paysEarningsNotBank(t *testing.T) {
	f := newFixture(t)
	creator := bech(1)
	seller := bech(2)
	buyer := bech(3)
	const price = 10000
	const bps = 500

	collection, err := f.cnftMsg.CreateCollection(f.ctx, &cnfttypes.MsgCreateCollection{
		Creator: creator, Name: "paintings", RoyaltyBps: bps,
	})
	require.NoError(t, err)
	tree, err := f.cnftMsg.CreateTree(f.ctx, &cnfttypes.MsgCreateTree{
		Creator: creator, CollectionId: collection.Id, Depth: 4, Buffer: 4, Canopy: 0,
	})
	require.NoError(t, err)
	body := leaf(t, creator, seller, assetID(7), "cid-sale", 0)
	_, err = f.cnftMsg.Mint(f.ctx, &cnfttypes.MsgMint{
		Creator: creator, TreeId: tree.Id, Root: tree.Root,
		Leaves: []cnfttypes.MintLeaf{{AssetId: body.AssetId, Owner: seller, MetadataCid: body.MetadataCid}},
	})
	require.NoError(t, err)
	proof := prove(t, [][]byte{mustHash(t, body)}, 0, 4)

	listing, err := f.msg.List(f.ctx, &types.MsgList{
		Seller: seller, TreeId: tree.Id, Leaf: body, Proof: proof, Price: math.NewInt(price),
	})
	require.NoError(t, err)
	stored, err := f.keeper.Listings.Get(f.ctx, listing.Id)
	require.NoError(t, err)
	require.Equal(t, creator, stored.RoyaltyCreator)
	require.Equal(t, uint32(bps), stored.RoyaltyBps)

	buyerAddr := mustAddr(t, buyer)
	sellerAddr := mustAddr(t, seller)
	creatorAddr := mustAddr(t, creator)
	f.bank.add(userKey(buyerAddr), math.NewInt(price))
	sellerBankBefore := f.bank.get(userKey(sellerAddr))
	creatorBankBefore := f.bank.get(userKey(creatorAddr))

	res, err := f.msg.Settle(f.ctx, &types.MsgSettle{
		Signer: buyer, ListingId: listing.Id, Leaf: body, Proof: proof,
	})
	require.NoError(t, err)
	require.True(t, res.Royalty.Equal(math.NewInt(500)))
	require.True(t, res.SellerProceeds.Equal(math.NewInt(9500)))
	require.True(t, f.earnings.get(creatorAddr).Equal(math.NewInt(500)))
	require.True(t, f.earnings.get(sellerAddr).Equal(math.NewInt(9500)))
	require.True(t, f.bank.get(userKey(sellerAddr)).Equal(sellerBankBefore), "seller bank balance must not increase")
	require.True(t, f.bank.get(userKey(creatorAddr)).Equal(creatorBankBefore), "creator bank balance must not increase")
	require.True(t, f.bank.get(userKey(buyerAddr)).IsZero())
	require.True(t, f.bank.get(modKey(types.ModuleName)).IsZero())
	require.True(t, f.bank.get(modKey("fees")).Equal(math.NewInt(price)))

	moved := body
	moved.Owner = buyer
	moved.Delegate = ""
	moved.Nonce = 1
	_, err = f.cnft.ProveOwned(f.ctx, tree.Id, moved, prove(t, [][]byte{mustHash(t, moved)}, 0, 4), buyerAddr)
	require.NoError(t, err)
}

func TestSettle_acceptsBidFromEscrow(t *testing.T) {
	f := newFixture(t)
	creator := bech(1)
	seller := bech(2)
	bidder := bech(4)
	collection, err := f.cnftMsg.CreateCollection(f.ctx, &cnfttypes.MsgCreateCollection{
		Creator: creator, Name: "paintings", RoyaltyBps: 500,
	})
	require.NoError(t, err)
	tree, err := f.cnftMsg.CreateTree(f.ctx, &cnfttypes.MsgCreateTree{
		Creator: creator, CollectionId: collection.Id, Depth: 4, Buffer: 4, Canopy: 0,
	})
	require.NoError(t, err)
	body := leaf(t, creator, seller, assetID(8), "cid-bid", 0)
	_, err = f.cnftMsg.Mint(f.ctx, &cnfttypes.MsgMint{
		Creator: creator, TreeId: tree.Id, Root: tree.Root,
		Leaves: []cnfttypes.MintLeaf{{AssetId: body.AssetId, Owner: seller, MetadataCid: body.MetadataCid}},
	})
	require.NoError(t, err)
	proof := prove(t, [][]byte{mustHash(t, body)}, 0, 4)
	listing, err := f.msg.List(f.ctx, &types.MsgList{
		Seller: seller, TreeId: tree.Id, Leaf: body, Proof: proof, Price: math.NewInt(10000),
	})
	require.NoError(t, err)

	bidderAddr := mustAddr(t, bidder)
	f.bank.add(userKey(bidderAddr), math.NewInt(2000))
	bid, err := f.msg.Bid(f.ctx, &types.MsgBid{Bidder: bidder, ListingId: listing.Id, Amount: math.NewInt(2000)})
	require.NoError(t, err)
	require.True(t, f.bank.get(userKey(bidderAddr)).IsZero())

	res, err := f.msg.Settle(f.ctx, &types.MsgSettle{
		Signer: seller, ListingId: listing.Id, BidId: bid.Id, Leaf: body, Proof: proof,
	})
	require.NoError(t, err)
	require.True(t, res.Price.Equal(math.NewInt(2000)))
	require.True(t, res.Royalty.Equal(math.NewInt(100)))
	require.True(t, res.SellerProceeds.Equal(math.NewInt(1900)))
	require.True(t, f.bank.get(userKey(mustAddr(t, seller))).IsZero())
	require.True(t, f.bank.get(userKey(mustAddr(t, creator))).IsZero())
	require.True(t, f.earnings.get(mustAddr(t, creator)).Equal(math.NewInt(100)))
	require.True(t, f.earnings.get(mustAddr(t, seller)).Equal(math.NewInt(1900)))
}

func TestProtoMarshalRoundTrip(t *testing.T) {
	msg := &types.MsgList{
		Seller: "orama1seller",
		TreeId: 3,
		Leaf: cnfttypes.Leaf{
			AssetId: assetID(1), Owner: "orama1owner", MetadataCid: "cid",
			CreatorHash: assetID(2), HashId: cnfttypes.HashIDSHA256,
		},
		Proof: cnfttypes.MerkleProof{Root: assetID(3), Index: 1, Siblings: [][]byte{assetID(4)}},
		Price: math.NewInt(10000),
	}
	bz, err := msg.Marshal()
	require.NoError(t, err)
	var out types.MsgList
	require.NoError(t, out.Unmarshal(bz))
	require.Equal(t, msg.Seller, out.Seller)
	require.True(t, out.Price.Equal(msg.Price))
	require.Equal(t, msg.Leaf.AssetId, out.Leaf.AssetId)
	require.Equal(t, msg.Proof.Root, out.Proof.Root)
}

func leaf(t *testing.T, creator, owner string, asset []byte, cid string, nonce uint64) cnfttypes.Leaf {
	t.Helper()
	addr, err := sdk.AccAddressFromBech32(creator)
	require.NoError(t, err)
	return cnfttypes.Leaf{
		AssetId: asset, Owner: owner, MetadataCid: cid,
		CreatorHash: cnfttypes.CreatorHash(addr), Nonce: nonce, HashId: cnfttypes.HashIDSHA256,
	}
}

func mustHash(t *testing.T, leaf cnfttypes.Leaf) []byte {
	t.Helper()
	hash, err := cnfttypes.HashLeaf(leaf)
	require.NoError(t, err)
	return hash
}

func prove(t *testing.T, leaves [][]byte, index int, depth uint32) cnfttypes.MerkleProof {
	t.Helper()
	siblings, root, err := cnfttypes.Proof(leaves, index, depth)
	require.NoError(t, err)
	return cnfttypes.MerkleProof{Root: root, Index: uint32(index), Siblings: siblings}
}

func mustAddr(t *testing.T, bech32 string) sdk.AccAddress {
	t.Helper()
	addr, err := sdk.AccAddressFromBech32(bech32)
	require.NoError(t, err)
	return addr
}

func TestRegisterInterfaces(t *testing.T) {
	reg := codectypes.NewInterfaceRegistry()
	require.NotPanics(t, func() { types.RegisterInterfaces(reg) })
}
