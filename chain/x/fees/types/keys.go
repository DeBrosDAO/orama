package types

import "cosmossdk.io/collections"

const (
	// ModuleName is the name of x/fees.
	ModuleName = "fees"

	// StoreKey is the store key for x/fees.
	StoreKey = ModuleName

	// DepositsModuleName is a SECOND module account, distinct from ModuleName, that holds only
	// locked state deposits (plans/open-network/track-c-chain.md C2's invariant list keeps "the
	// deposit module balance == open deposits" separate from "sum of earnings balances == the
	// earnings module balance" - two independently checkable balances, not one mixed pool).
	DepositsModuleName = "fees_deposits"
)

var (
	// ParamsKey is the collections key for the module's genesis-only Params.
	ParamsKey = collections.NewPrefix(0)
	// BaseFeeKey is the collections key for the current per-gas-unit base fee.
	BaseFeeKey = collections.NewPrefix(1)
	// EarningsPrefix is the collections key prefix for per-address earnings balances, keyed by
	// bech32 account address.
	EarningsPrefix = collections.NewPrefix(2)
	// DepositsPrefix is the collections key prefix for open state-deposit ledger entries, keyed by
	// the caller-assigned deposit id.
	DepositsPrefix = collections.NewPrefix(3)
)
