//go:build e2e_fleet

package chainassets

import (
	"fmt"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
)

func marketMsg(typ string, fields map[string]any) chain.Msg {
	return chain.NewMsg("/orama.market.v1."+typ, fields)
}

// listingView is `oramad query market listing`.
type listingView struct {
	Listing struct {
		Seller         string    `json:"seller"`
		Price          chain.Int `json:"price"`
		RoyaltyCreator string    `json:"royalty_creator"`
		RoyaltyBps     int       `json:"royalty_bps"`
	} `json:"listing"`
}

// TestMarket_listBidSettleRules: the owner of a compressed NFT lists it with a
// proof; the listing carries the collection's royalty (2.5%) and creator; a
// second listing of the same asset is refused; the seller cannot bid on or
// buy their own listing; a bid and a direct buy are paid from the BANK
// balance, which no run account holds (funds.go), so both are refused and the
// royalty split to earnings on a sale is blocked; a bid that does not exist
// cannot be cancelled or accepted; only the seller cancels the listing.
func TestMarket_listBidSettleRules(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	seller := c.FundedValidator(t, 0, chain.Orama(1))
	buyer := c.FundedValidator(t, 2, chain.Orama(1))
	f := newFixture(t, c, seller)
	x := f.mint(t, seller.Address)[0]
	price := chain.Orama(5)
	list := func() chain.Result {
		return c.Submit(t, seller, chain.TxOptions{}, marketMsg("MsgList", map[string]any{"seller": seller.Address,
			"tree_id": fmt.Sprint(f.tree), "leaf": x.leaf(), "proof": f.model.proof(x.index), "price": price.String()}))
	}
	r := chain.RequireOK(t, "list", list())
	id := fmt.Sprint(firstResponseID(t, r))
	t.Cleanup(func() { cancelListingAtCleanup(t, c, seller, id) })
	var v listingView
	c.Query(t, seller.Node, &v, "market", "listing", id)
	if v.Listing.Seller != seller.Address || v.Listing.Price.Cmp(price) != 0 || v.Listing.RoyaltyBps != royaltyBps || v.Listing.RoyaltyCreator != seller.Address {
		t.Errorf("listing %+v", v.Listing)
	}
	chain.RequireRefused(t, "list the same asset twice", list(), "asset is already listed")
	requireBidAndSettleRefusals(t, c, f, x, id, seller, buyer, price)
	chain.RequireRefused(t, "cancel by someone else", c.Submit(t, buyer, chain.TxOptions{}, cancelListingMsg(buyer.Address, id)), "signer is not the seller")
	chain.RequireOK(t, "cancel by the seller", c.Submit(t, seller, chain.TxOptions{}, cancelListingMsg(seller.Address, id)))
	if out := c.QueryFails(t, seller.Node, "market", "listing", id); !chain.NotFound(out) {
		t.Errorf("listing still readable after cancel: %s", out)
	}
	again := chain.RequireOK(t, "list again after cancel", list())
	id2 := fmt.Sprint(firstResponseID(t, again))
	t.Cleanup(func() { cancelListingAtCleanup(t, c, seller, id2) })
	if id2 == id {
		t.Errorf("a new listing reused the cancelled id %s", id)
	}
	c.RequireInvariants(t, "market refusals")
}

func cancelListingMsg(seller, id string) chain.Msg {
	return marketMsg("MsgCancelListing", map[string]any{"seller": seller, "listing_id": id})
}

func cancelListingAtCleanup(t *testing.T, c *chain.Chain, seller chain.Key, id string) {
	out := c.QueryOut(t, seller.Node, "market", "listing", id)
	if out.Exit != 0 {
		return
	}
	c.CleanupSubmit(t, seller, "cancel listing "+id, cancelListingMsg(seller.Address, id))
}

// requireBidAndSettleRefusals: self-bid and self-buy refused, a bid and a
// direct buy refused for the empty bank, and bids that do not exist cannot
// be accepted, cancelled or read.
func requireBidAndSettleRefusals(t *testing.T, c *chain.Chain, f *cnftFixture, x asset, id string, seller, buyer chain.Key, price chain.Int) {
	t.Helper()
	bid := func(k chain.Key) chain.Result {
		return c.Submit(t, k, chain.TxOptions{}, marketMsg("MsgBid", map[string]any{"bidder": k.Address, "listing_id": id, "amount": price.String()}))
	}
	chain.RequireRefused(t, "seller bids", bid(seller), "seller cannot bid on their own listing")
	chain.RequireRefused(t, "bid from an empty bank", bid(buyer), "failed to escrow bid", "insufficient funds")
	settle := func(k chain.Key, bidID string) chain.Result {
		return c.Submit(t, k, chain.TxOptions{}, marketMsg("MsgSettle", map[string]any{"signer": k.Address, "listing_id": id,
			"bid_id": bidID, "leaf": x.leaf(), "proof": f.model.proof(x.index)}))
	}
	chain.RequireRefused(t, "seller buys", settle(seller, "0"), "seller cannot buy their own listing")
	chain.RequireRefused(t, "buy from an empty bank", settle(buyer, "0"), "is below price")
	chain.RequireRefused(t, "accept a bid that does not exist", settle(seller, "424242"), "bid 424242 does not exist")
	chain.RequireRefused(t, "buyer accepts a bid", settle(buyer, "424242"), "only the seller can accept a bid")
	cancelBid := marketMsg("MsgCancelBid", map[string]any{"bidder": buyer.Address, "listing_id": id, "bid_id": "424242"})
	chain.RequireRefused(t, "cancel a bid that does not exist", c.Submit(t, buyer, chain.TxOptions{}, cancelBid), "bid 424242 does not exist")
	if out := c.QueryFails(t, buyer.Node, "market", "bid", id, "424242"); !chain.NotFound(out) {
		t.Errorf("bid query of a bid that does not exist: %s", out)
	}
}
