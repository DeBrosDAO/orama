package types

import "cosmossdk.io/collections"

const (
	// ModuleName is the name of x/emission.
	ModuleName = "emission"

	// StoreKey is the store key for x/emission.
	StoreKey = ModuleName
)

var (
	// ParamsKey is the collections key for the module's genesis-only Params.
	ParamsKey = collections.NewPrefix(0)
	// EpochStateKey is the collections key for the module's mutable EpochState.
	EpochStateKey = collections.NewPrefix(1)
	// CeilingsPrefix is the collections key prefix for the bounded window of per-epoch
	// CeilingRecord entries, keyed by epoch number.
	CeilingsPrefix = collections.NewPrefix(2)
)
