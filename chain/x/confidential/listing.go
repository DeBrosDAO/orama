package confidential

import (
	"errors"
	"fmt"
)

// MarketplaceLive is false. Track H's marketplace (H3) needs real attestation
// first; until then no listing can be leased.
const MarketplaceLive = false

// ErrMarketplaceNotLive is returned by every trading path.
var ErrMarketplaceNotLive = errors.New("the confidential marketplace is not live: no attestation verifier is linked")

// ListingTerms are the resources and price a confidential node offers.
type ListingTerms struct {
	CPUMillis          uint64
	MemoryMiB          uint64
	PriceNoramaPerHour uint64
}

// Listing offers a confidential node's capacity. NewListing is its only
// constructor and needs an attested node.
type Listing struct {
	Node  Node
	Terms ListingTerms
}

// NewListing lists node's capacity. It refuses a node without a verified report.
func NewListing(node Node, terms ListingTerms) (Listing, error) {
	if !node.Attested() {
		return Listing{}, fmt.Errorf("listing node %q: %w", node.NodeID, ErrUnverifiedReport)
	}
	if terms.CPUMillis == 0 || terms.MemoryMiB == 0 || terms.PriceNoramaPerHour == 0 {
		return Listing{}, fmt.Errorf("listing terms need CPU, memory and a price")
	}
	return Listing{Node: node, Terms: terms}, nil
}

// Lease refuses: the marketplace is not live.
func (l Listing) Lease(lessee string) error {
	_ = lessee
	return ErrMarketplaceNotLive
}
