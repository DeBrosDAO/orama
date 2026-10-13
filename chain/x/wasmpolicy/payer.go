package wasmpolicy

import (
	sdk "github.com/cosmos/cosmos-sdk/types"
)

type payerKey struct{}

// WithDepositPayer returns ctx carrying the account that pays contract state deposits for the
// messages that run under it. The ante chain sets it to the transaction's first signer.
func WithDepositPayer(ctx sdk.Context, payer sdk.AccAddress) sdk.Context {
	return ctx.WithValue(payerKey{}, payer)
}

// DepositPayer returns the account WithDepositPayer set, or nil when none was set (genesis,
// begin and end blockers, and direct keeper calls in tests).
func DepositPayer(ctx sdk.Context) sdk.AccAddress {
	payer, _ := ctx.Value(payerKey{}).(sdk.AccAddress)
	return payer
}
