package types

import "cosmossdk.io/collections"

const (
	// ModuleName is the market module account. Settlement moves sale proceeds
	// out of this account through EarningsKeeper.CreditEarnings.
	ModuleName = "market"

	// StoreKey is the store key for x/market.
	StoreKey = ModuleName

	// BasisPoints is 100% of a sale price.
	BasisPoints uint32 = 10_000
)

var (
	// NextListingIDKey is the next listing id to assign.
	NextListingIDKey = collections.NewPrefix(0)
	// NextBidIDKey is the next bid id to assign.
	NextBidIDKey = collections.NewPrefix(1)
	// ListingsPrefix keys listings by id.
	ListingsPrefix = collections.NewPrefix(2)
	// BidsPrefix keys bids by (listing id, bid id).
	BidsPrefix = collections.NewPrefix(3)
	// AssetListingPrefix keys the active listing id of an asset.
	AssetListingPrefix = collections.NewPrefix(4)
)
