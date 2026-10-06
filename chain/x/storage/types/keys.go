package types

import "cosmossdk.io/collections"

const (
	// ModuleName is the name of x/storage. It is also the module account that
	// receives mints before the service split.
	ModuleName = "storage"

	// StoreKey is the store key for x/storage.
	StoreKey = ModuleName

	// EscrowModuleName holds deal escrow. Its balance equals the sum of open
	// deals' remaining escrow (C7 escrow conservation).
	EscrowModuleName = "storage_escrow"

	// ArchiveModuleName holds the 5% archive-fund share of service payments
	// and tops up ARCHIVE deals when the storage ceiling is short.
	ArchiveModuleName = "storage_archive"

	// MinReplicas is the hard-coded replica count. A deal needs this many
	// distinct operators. It is not a parameter.
	MinReplicas uint32 = 3

	// MaxReplicas bounds a single deal so escrow math stays inside int64.
	MaxReplicas uint32 = 32

	// MaxDurationEpochs bounds deal length.
	MaxDurationEpochs uint64 = 1_000_000

	// NonceLen is the required deal_nonce width.
	NonceLen = 32

	// RootLen is a SHA-256 piece root.
	RootLen = 32

	// MaxNodeIDLen bounds node and operator ids stored in keys.
	MaxNodeIDLen = 128
)

var (
	ParamsKey            = collections.NewPrefix(0)
	NextDealIDKey        = collections.NewPrefix(1)
	LastEpochKey         = collections.NewPrefix(2)
	LastProtocolEpochKey = collections.NewPrefix(3)
	QueueHeadKey         = collections.NewPrefix(4)
	QueueTailKey         = collections.NewPrefix(5)
	ArchiveFundKey       = collections.NewPrefix(6)
	DealCountHeightKey   = collections.NewPrefix(7)
	DealsInBlockKey      = collections.NewPrefix(8)
	DealsPrefix          = collections.NewPrefix(9)
	SlotsPrefix          = collections.NewPrefix(10)
	AuthsPrefix          = collections.NewPrefix(11)
	NodesPrefix          = collections.NewPrefix(12)
	PendingPrefix        = collections.NewPrefix(13)
	ReplicaCountPrefix   = collections.NewPrefix(14)
	ReplicaAtPrefix      = collections.NewPrefix(15)
	RechallengePrefix    = collections.NewPrefix(16)
	ChallengesPrefix     = collections.NewPrefix(17)
	QueuePrefix          = collections.NewPrefix(18)
	QueuePendingPrefix   = collections.NewPrefix(19)
	ReservedPrefix       = collections.NewPrefix(20)
	EpochMintPrefix      = collections.NewPrefix(21)
	OperatorMintPrefix   = collections.NewPrefix(22)
	ReleasesPrefix       = collections.NewPrefix(23)
	ProbationNodePrefix  = collections.NewPrefix(24)
	ProbationOpPrefix    = collections.NewPrefix(25)
	ProbationNetPrefix   = collections.NewPrefix(26)
	ProbationASNPrefix   = collections.NewPrefix(27)
	FailuresPrefix       = collections.NewPrefix(28)
)
