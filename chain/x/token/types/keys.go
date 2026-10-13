package types

import "cosmossdk.io/collections"

const (
	// ModuleName is the name of x/token.
	ModuleName = "token"

	// StoreKey is the store key for x/token.
	StoreKey = ModuleName

	// DenomPrefix is the first segment of every factory denom:
	// factory/{creator bech32}/{subdenom}.
	DenomPrefix = "factory"

	// TransferHookGasCap is the most gas a transfer hook may consume.
	// A hook that asks for more fails the transfer. This is not a CosmWasm
	// meter; the keeper installs a gas meter of this size around the callback.
	TransferHookGasCap uint64 = 100_000

	// MaxTransferFeeBps is 100% of a transfer, in basis points. The fee is a
	// share of the token itself and is burned.
	MaxTransferFeeBps uint32 = 10_000

	// MinSubdenomLen and MaxSubdenomLen bound the subdenom segment.
	MinSubdenomLen = 2
	MaxSubdenomLen = 44

	// MaxNameLen, MaxSymbolLen and MaxDescriptionLen bound the metadata the
	// state deposit covers.
	MaxNameLen        = 64
	MaxSymbolLen      = 16
	MaxDescriptionLen = 256
)

var (
	// ParamsKey is the collections key for genesis-only Params.
	ParamsKey = collections.NewPrefix(0)
	// TokensPrefix is the collections key prefix for token records, keyed by denom.
	TokensPrefix = collections.NewPrefix(1)
	// FrozenPrefix is the collections key prefix for frozen (denom, account) pairs.
	FrozenPrefix = collections.NewPrefix(2)
)
