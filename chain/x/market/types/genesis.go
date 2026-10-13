package types

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// DefaultGenesisState returns an empty x/market genesis. Ids start at 1.
func DefaultGenesisState() *GenesisState {
	return &GenesisState{
		Listings:      []Listing{},
		Bids:          []Bid{},
		NextListingId: 1,
		NextBidId:     1,
	}
}

// Validate checks genesis listings and bids.
func (gs GenesisState) Validate() error {
	if gs.NextListingId == 0 || gs.NextBidId == 0 {
		return fmt.Errorf("next listing and bid ids must be positive")
	}
	listings := make(map[uint64]Listing, len(gs.Listings))
	assets := make(map[string]struct{}, len(gs.Listings))
	var maxListing uint64
	for _, listing := range gs.Listings {
		if listing.Id == 0 {
			return fmt.Errorf("listing id is required")
		}
		if _, ok := listings[listing.Id]; ok {
			return fmt.Errorf("duplicate listing id %d", listing.Id)
		}
		if err := validateBech32("seller", listing.Seller); err != nil {
			return fmt.Errorf("listing %d: %w", listing.Id, err)
		}
		if err := validateBech32("royalty creator", listing.RoyaltyCreator); err != nil {
			return fmt.Errorf("listing %d: %w", listing.Id, err)
		}
		if listing.RoyaltyBps > BasisPoints {
			return fmt.Errorf("listing %d royalty_bps %d exceeds %d", listing.Id, listing.RoyaltyBps, BasisPoints)
		}
		if err := positive("price", listing.Price); err != nil {
			return fmt.Errorf("listing %d: %w", listing.Id, err)
		}
		if len(listing.AssetId) != 32 {
			return fmt.Errorf("listing %d asset_id must be 32 bytes", listing.Id)
		}
		key := string(listing.AssetId)
		if _, ok := assets[key]; ok {
			return fmt.Errorf("duplicate listing for one asset")
		}
		assets[key] = struct{}{}
		listings[listing.Id] = listing
		if listing.Id > maxListing {
			maxListing = listing.Id
		}
	}
	if maxListing >= gs.NextListingId {
		return fmt.Errorf("next_listing_id %d is not greater than existing id %d", gs.NextListingId, maxListing)
	}

	seenBids := make(map[uint64]struct{}, len(gs.Bids))
	var maxBid uint64
	for _, bid := range gs.Bids {
		if bid.Id == 0 {
			return fmt.Errorf("bid id is required")
		}
		if _, ok := seenBids[bid.Id]; ok {
			return fmt.Errorf("duplicate bid id %d", bid.Id)
		}
		seenBids[bid.Id] = struct{}{}
		listing, ok := listings[bid.ListingId]
		if !ok {
			return fmt.Errorf("bid %d references unknown listing %d", bid.Id, bid.ListingId)
		}
		if err := validateBech32("bidder", bid.Bidder); err != nil {
			return fmt.Errorf("bid %d: %w", bid.Id, err)
		}
		if err := positive("amount", bid.Amount); err != nil {
			return fmt.Errorf("bid %d: %w", bid.Id, err)
		}
		same, err := sameAccount(bid.Bidder, listing.Seller)
		if err != nil {
			return err
		}
		if same {
			return fmt.Errorf("bid %d is from the seller", bid.Id)
		}
		if bid.Id > maxBid {
			maxBid = bid.Id
		}
	}
	if maxBid >= gs.NextBidId {
		return fmt.Errorf("next_bid_id %d is not greater than existing id %d", gs.NextBidId, maxBid)
	}
	return nil
}

func sameAccount(a, b string) (bool, error) {
	aa, err := sdk.AccAddressFromBech32(a)
	if err != nil {
		return false, fmt.Errorf("address %q: %w", a, err)
	}
	bb, err := sdk.AccAddressFromBech32(b)
	if err != nil {
		return false, fmt.Errorf("address %q: %w", b, err)
	}
	return aa.Equals(bb), nil
}
