package keeper

import (
	"fmt"

	"cosmossdk.io/collections"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/market/types"
)

// InitGenesis writes x/market's genesis state.
func (k Keeper) InitGenesis(ctx sdk.Context, genState types.GenesisState) error {
	if err := genState.Validate(); err != nil {
		return fmt.Errorf("invalid market genesis state: %w", err)
	}
	if err := k.NextListingID.Set(ctx, genState.NextListingId); err != nil {
		return fmt.Errorf("failed to set next listing id: %w", err)
	}
	if err := k.NextBidID.Set(ctx, genState.NextBidId); err != nil {
		return fmt.Errorf("failed to set next bid id: %w", err)
	}
	for _, listing := range genState.Listings {
		if err := k.Listings.Set(ctx, listing.Id, listing); err != nil {
			return fmt.Errorf("failed to set listing %d: %w", listing.Id, err)
		}
		if err := k.AssetListing.Set(ctx, listing.AssetId, listing.Id); err != nil {
			return fmt.Errorf("failed to index listing %d: %w", listing.Id, err)
		}
	}
	for _, bid := range genState.Bids {
		if err := k.Bids.Set(ctx, collections.Join(bid.ListingId, bid.Id), bid); err != nil {
			return fmt.Errorf("failed to set bid %d: %w", bid.Id, err)
		}
	}
	return nil
}

// ExportGenesis reads x/market's current state.
func (k Keeper) ExportGenesis(ctx sdk.Context) (*types.GenesisState, error) {
	nextListing, err := k.NextListingID.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get next listing id: %w", err)
	}
	nextBid, err := k.NextBidID.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get next bid id: %w", err)
	}
	var listings []types.Listing
	if err := k.Listings.Walk(ctx, nil, func(_ uint64, listing types.Listing) (bool, error) {
		listings = append(listings, listing)
		return false, nil
	}); err != nil {
		return nil, fmt.Errorf("failed to walk listings: %w", err)
	}
	var bids []types.Bid
	if err := k.Bids.Walk(ctx, nil, func(_ collections.Pair[uint64, uint64], bid types.Bid) (bool, error) {
		bids = append(bids, bid)
		return false, nil
	}); err != nil {
		return nil, fmt.Errorf("failed to walk bids: %w", err)
	}
	if listings == nil {
		listings = []types.Listing{}
	}
	if bids == nil {
		bids = []types.Bid{}
	}
	return &types.GenesisState{
		Listings:      listings,
		Bids:          bids,
		NextListingId: nextListing,
		NextBidId:     nextBid,
	}, nil
}
