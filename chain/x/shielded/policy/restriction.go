package policy

import (
	"context"

	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
)

// NoramaSendRestriction refuses a bank send of norama between two user
// accounts. Module accounts may still move norama. moduleAddresses is the
// set of bech32 module-account addresses, the same set BlockedAddresses uses.
func NoramaSendRestriction(moduleAddresses map[string]bool) banktypes.SendRestrictionFn {
	return func(_ context.Context, from, to sdk.AccAddress, amt sdk.Coins) (sdk.AccAddress, error) {
		if amt.AmountOf(BaseDenom).IsZero() {
			return to, nil
		}
		if moduleAddresses[from.String()] || moduleAddresses[to.String()] {
			return to, nil
		}
		return nil, ErrPublicPayment
	}
}
