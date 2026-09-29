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

	// DefaultDepositPerByte is the genesis price of one byte of contract storage, in norama:
	// P3's about 0.07 ORAMA per KB, 0.07 * 10^9 / 1024 with integer division. It is the same
	// price x/token and x/nodes ship. It is not a price oracle.
	DefaultDepositPerByte int64 = 68_359

	// DefaultMaxDepositPerTx caps the state deposit one transaction may lock on its signer through
	// contract storage growth: 10 ORAMA, about 146 KB of new state at the default price.
	DefaultMaxDepositPerTx int64 = 10_000_000_000

	// DefaultMaxDepositChunks bounds the deposit rows of one contract: one per payer. A shrink walks
	// at most this many, so it always fits in a block.
	DefaultMaxDepositChunks uint64 = 32

	// DefaultChunkOverheadBytes prices the ledger row and the x/fees deposit row a new payer adds to a
	// contract, in bytes of storage: it is locked with the chunk and released with it.
	DefaultChunkOverheadBytes uint64 = 512

	// DefaultUploadSunsetHeight is the genesis upload_sunset_height (P6).
	DefaultUploadSunsetHeight uint64 = SunsetDays * BlocksPerDay
)

var (
	// SunsetPrefix stores the single upload_sunset_height.
	SunsetPrefix = collections.NewPrefix(0)
	// CodePrefix stores the genesis code id set.
	CodePrefix = collections.NewPrefix(1)
	// DepositPerBytePrefix stores the single deposit_per_byte.
	DepositPerBytePrefix = collections.NewPrefix(2)
	// ChunkPrefix stores the deposit chunks, keyed by (contract, sequence).
	ChunkPrefix = collections.NewPrefix(3)
	// ContractBytesPrefix stores the charged byte count of each contract.
	ContractBytesPrefix = collections.NewPrefix(4)
	// LimitsPrefix stores the single deposit Limits.
	LimitsPrefix = collections.NewPrefix(6)
	// NextChunkPrefix stores the next chunk sequence.
	NextChunkPrefix = collections.NewPrefix(5)
)
