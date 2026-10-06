package indexer

import (
	"encoding/hex"
	"fmt"

	gogoproto "github.com/cosmos/gogoproto/proto"

	cnfttypes "github.com/DeBrosOfficial/network/chain/x/cnft/types"
	markettypes "github.com/DeBrosOfficial/network/chain/x/market/types"
)

const (
	queryDecompressed = "/orama.cnft.v1.Query/Decompressed"
	queryListing      = "/orama.market.v1.Query/Listing"
	queryBid          = "/orama.market.v1.Query/Bid"
)

func applyList(a applier, msg, resp gogoproto.Message) error {
	m, r := msg.(*markettypes.MsgList), resp.(*markettypes.MsgListResponse)
	return a.w.putListing(r.Id, listing{TreeID: m.TreeId})
}

func applyBid(a applier, msg, resp gogoproto.Message) error {
	m, r := msg.(*markettypes.MsgBid), resp.(*markettypes.MsgBidResponse)
	return a.w.putBid(m.ListingId, r.Id, m.Bidder)
}

func applyCancelListing(a applier, msg, _ gogoproto.Message) error {
	return a.w.deleteListing(msg.(*markettypes.MsgCancelListing).ListingId)
}

func applyCancelBid(a applier, msg, _ gogoproto.Message) error {
	m := msg.(*markettypes.MsgCancelBid)
	return a.w.deleteBid(m.ListingId, m.BidId)
}

// applySettle moves the listed leaf to the buyer the way x/market does: the
// signer when bid_id is 0, the bidder otherwise, with the delegate cleared
// and the nonce advanced.
func applySettle(a applier, msg, _ gogoproto.Message) error {
	m := msg.(*markettypes.MsgSettle)
	tree, err := a.listingTree(m.ListingId)
	if err != nil {
		return err
	}
	buyer := m.Signer
	if m.BidId != 0 {
		if buyer, err = a.bidder(m.ListingId, m.BidId); err != nil {
			return err
		}
	}
	next := m.Leaf
	next.Owner, next.Delegate, next.Nonce = buyer, "", m.Leaf.Nonce+1
	if err := a.putLeaf(next, tree, m.Proof.Index, AssetCompressed); err != nil {
		return err
	}
	return a.w.deleteListing(m.ListingId)
}

// The lookups below read what an earlier message wrote. The index holds it
// when that message came after the start height, including earlier in the
// same block. Otherwise the record was already in the chain state before
// this block, so it is read at height-1: settle and compress delete it, and
// the latest state no longer has it. A node that pruned that state answers
// with an error, and the block is not indexed.

func (a applier) listingTree(id uint64) (uint64, error) {
	l, ok, err := a.w.listing(id)
	if err != nil || ok {
		return l.TreeID, err
	}
	var resp markettypes.QueryListingResponse
	if err := a.queryBefore(queryListing, &markettypes.QueryListingRequest{Id: id}, &resp); err != nil {
		return 0, fmt.Errorf("read listing %d: %w", id, err)
	}
	return resp.Listing.TreeId, nil
}

func (a applier) bidder(listingID, bidID uint64) (string, error) {
	who, ok, err := a.w.bidder(listingID, bidID)
	if err != nil || ok {
		return who, err
	}
	var resp markettypes.QueryBidResponse
	if err := a.queryBefore(queryBid, &markettypes.QueryBidRequest{ListingId: listingID, BidId: bidID}, &resp); err != nil {
		return "", fmt.Errorf("read bid %d on listing %d: %w", bidID, listingID, err)
	}
	return resp.Bid.Bidder, nil
}

// decompressedAsset returns the decompressed asset a compress consumes, and
// drops its record from the index: the asset moves to a new leaf.
func (a applier) decompressedAsset(id []byte) (cnfttypes.DecompressedAsset, error) {
	loc, rec, ok, err := a.w.decompressed(id)
	if err != nil {
		return cnfttypes.DecompressedAsset{}, err
	}
	if ok {
		creatorHash, err := hex.DecodeString(rec.CreatorHash)
		if err != nil {
			return cnfttypes.DecompressedAsset{}, fmt.Errorf("asset %x creator hash in the index: %w", id, err)
		}
		out := cnfttypes.DecompressedAsset{
			AssetId: id, Delegate: rec.Delegate, MetadataCid: rec.MetadataCID,
			CreatorHash: creatorHash, HashId: rec.HashID,
		}
		return out, a.w.deleteAsset(loc)
	}
	var resp cnfttypes.QueryDecompressedResponse
	if err := a.queryBefore(queryDecompressed, &cnfttypes.QueryDecompressedRequest{AssetId: id}, &resp); err != nil {
		return cnfttypes.DecompressedAsset{}, fmt.Errorf("read decompressed asset %x: %w", id, err)
	}
	return resp.Asset, nil
}

func (a applier) queryBefore(method string, req, resp gogoproto.Message) error {
	if a.height < 2 {
		return fmt.Errorf("no state before height %d", a.height)
	}
	return a.chain.QueryAt(a.ctx, a.height-1, method, req, resp)
}
