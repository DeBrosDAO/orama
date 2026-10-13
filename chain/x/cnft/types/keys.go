package types

import "cosmossdk.io/collections"

const (
	// ModuleName is the name of x/cnft.
	ModuleName = "cnft"

	// StoreKey is the store key for x/cnft.
	StoreKey = ModuleName

	// HashSize is the width of every Merkle node and leaf hash.
	HashSize = 32

	// HashIDSHA256 is the hash_id written into every leaf. The leaf hash is
	// SHA-256 of EncodeLeaf; the id is inside that encoding so a later hash
	// can be distinguished without reinterpreting old leaves.
	HashIDSHA256 uint32 = 1

	// MinDepth and MaxDepth bound a tree. MaxDepth is 30 because the changelog
	// update isolates the critical bit inside a uint32.
	MinDepth uint32 = 1
	MaxDepth uint32 = 30

	// MaxBuffer is the largest changelog ring a tree may keep.
	MaxBuffer uint32 = 2048

	// MaxCanopy is the deepest canopy, measured in levels below the root.
	// 14 levels is 2^15-2 cached nodes.
	MaxCanopy uint32 = 14

	// MaxMintBatch is the most leaves one MsgMint may append.
	MaxMintBatch = 64

	// MaxCIDLen is the longest metadata or snapshot CID accepted.
	MaxCIDLen = 128

	// MaxNameLen is the longest collection name accepted.
	MaxNameLen = 64

	// RoyaltyBasisPoints is 100%. A collection royalty must be at most this.
	RoyaltyBasisPoints uint32 = 10_000
)

const (
	treeHeaderBytes = 128
	nodeBytes       = HashSize
	indexBytes      = 4
)

var (
	// NextCollectionIDKey is the next collection id to assign.
	NextCollectionIDKey = collections.NewPrefix(0)
	// NextTreeIDKey is the next tree id to assign.
	NextTreeIDKey = collections.NewPrefix(1)
	// CollectionsPrefix keys collections by id.
	CollectionsPrefix = collections.NewPrefix(2)
	// TreesPrefix keys trees by id.
	TreesPrefix = collections.NewPrefix(3)
	// DecompressedPrefix keys decompressed assets by asset id.
	DecompressedPrefix = collections.NewPrefix(4)
	// SnapshotsPrefix keys snapshot records by (tree id, snapshot id).
	SnapshotsPrefix = collections.NewPrefix(5)
	// NextSnapshotPrefix keys the next per-tree snapshot id.
	NextSnapshotPrefix = collections.NewPrefix(6)
)
