package types

import "cosmossdk.io/collections"

const (
	// ModuleName is the name of x/archive.
	ModuleName = "archive"

	// StoreKey is the store key for x/archive.
	StoreKey = ModuleName
)

var (
	// ParamsKey is the collections key for genesis-only Params.
	ParamsKey = collections.NewPrefix(0)
	// LastArchivedHeightKey is the collections key for the contiguous archived prefix.
	LastArchivedHeightKey = collections.NewPrefix(1)
	// RangesPrefix is the collections prefix for range records, keyed by (start, end).
	RangesPrefix = collections.NewPrefix(2)
	// AttachedDealsPrefix maps each attached deal id to its range, so one deal
	// cannot be counted as a replica of two ranges.
	AttachedDealsPrefix = collections.NewPrefix(3)
)
