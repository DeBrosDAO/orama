package types

import (
	"fmt"

	"cosmossdk.io/math"
)

// SplitSale divides a sale price into a creator royalty and the seller's
// remainder. The royalty is price * bps / 10000, rounded down; the seller
// receives whatever is left, so the two shares sum to price.
func SplitSale(price math.Int, royaltyBps uint32) (creator, seller math.Int, err error) {
	if price.IsNil() || !price.IsPositive() {
		return math.Int{}, math.Int{}, fmt.Errorf("price must be positive")
	}
	if royaltyBps > BasisPoints {
		return math.Int{}, math.Int{}, fmt.Errorf("royalty_bps %d exceeds %d", royaltyBps, BasisPoints)
	}
	creator = price.MulRaw(int64(royaltyBps)).QuoRaw(int64(BasisPoints))
	seller = price.Sub(creator)
	return creator, seller, nil
}
