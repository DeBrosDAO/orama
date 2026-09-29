package policy

import (
	"context"

	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
)

// ContractCheck reports whether addr is a contract account. A contract that wasmd is
// instantiating right now, and is funding before it registers, counts as one.
type ContractCheck func(ctx context.Context, addr sdk.AccAddress) bool

// NoramaSendRestriction refuses a public norama payment.
//
// Module accounts may move norama in either direction. A user or a contract
// may pay a contract. A user may not pay another user, and a
// contract may not pay a user: that payout belongs in an earnings account.
// isContract decides what a contract is. Nil means there are none, as in a binary built
// without the wasm VM.
func NoramaSendRestriction(moduleAddresses map[string]bool, isContract ContractCheck) banktypes.SendRestrictionFn {
	return func(ctx context.Context, from, to sdk.AccAddress, amt sdk.Coins) (sdk.AccAddress, error) {
		if amt.AmountOf(BaseDenom).IsZero() {
			return to, nil
		}
		if moduleAddresses[from.String()] || moduleAddresses[to.String()] {
			return to, nil
		}
		if isContract != nil && isContract(ctx, to) {
			return to, nil
		}
		return nil, ErrPublicPayment
	}
}
