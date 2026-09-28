package policy

import (
	"context"

	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
)

// NoramaSendRestriction refuses a public norama payment.
//
// Module accounts may move norama in either direction. A user or a contract
// may pay a registered contract. A user may not pay another user, and a
// contract may not pay a user: that payout belongs in an earnings account.
// contracts is the set of bech32 contract addresses. Nil means none are
// registered yet.
func NoramaSendRestriction(moduleAddresses, contracts map[string]bool) banktypes.SendRestrictionFn {
	return func(_ context.Context, from, to sdk.AccAddress, amt sdk.Coins) (sdk.AccAddress, error) {
		if amt.AmountOf(BaseDenom).IsZero() {
			return to, nil
		}
		if moduleAddresses[from.String()] || moduleAddresses[to.String()] || contracts[to.String()] {
			return to, nil
		}
		return nil, ErrPublicPayment
	}
}
