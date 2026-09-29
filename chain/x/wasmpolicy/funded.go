package wasmpolicy

import (
	sdk "github.com/cosmos/cosmos-sdk/types"
)

type fundedKey struct{}

// WithFundedContract returns ctx carrying the address wasmd is transferring funds to as a contract.
// wasmd's instantiate moves the attached funds before it registers the new contract, so for that
// one transfer the recipient is not yet a registered contract. Only the wasm coin transferrer sets
// this, and only for the recipient of that transfer: every wasmd caller of TransferCoins names a
// contract or one being instantiated.
func WithFundedContract(ctx sdk.Context, contract sdk.AccAddress) sdk.Context {
	return ctx.WithValue(fundedKey{}, contract)
}

// IsFundedContract reports whether addr is the contract WithFundedContract marked on ctx.
func IsFundedContract(ctx interface{ Value(key any) any }, addr sdk.AccAddress) bool {
	marked, _ := ctx.Value(fundedKey{}).(sdk.AccAddress)
	return len(marked) > 0 && marked.Equals(addr)
}
