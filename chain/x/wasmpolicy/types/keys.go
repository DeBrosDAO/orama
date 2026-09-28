package types

import "cosmossdk.io/collections"

const (
	// ModuleName is the Orama wasm policy module. It is not wasmd's "wasm" module:
	// that name belongs to x/wasm when the cgo build links libwasmvm.
	ModuleName = "wasmpolicy"

	// StoreKey is the KV store key for upload_sunset_height and the genesis code set.
	StoreKey = ModuleName

	// BaseDenom is the bank denom a contract may not send to a user account.
	BaseDenom = "norama"

	// AssumedBlockSeconds is the block interval DefaultUploadSunsetHeight is counted in.
	// oramad does not override CometBFT's timeout_commit; the SDK default is 5s.
	AssumedBlockSeconds = 5

	// SunsetDays is P6: about six months after genesis.
	SunsetDays = 183

	// BlocksPerDay is the number of AssumedBlockSeconds blocks in a day.
	BlocksPerDay = 24 * 60 * 60 / AssumedBlockSeconds

	// DefaultUploadSunsetHeight is the genesis upload_sunset_height (P6).
	DefaultUploadSunsetHeight uint64 = SunsetDays * BlocksPerDay
)

var (
	// SunsetPrefix stores the single upload_sunset_height.
	SunsetPrefix = collections.NewPrefix(0)
	// CodePrefix stores the genesis code id set.
	CodePrefix = collections.NewPrefix(1)
)
