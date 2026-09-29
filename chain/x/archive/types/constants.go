package types

// Quorum and hash sizes from plans/open-network/track-c-chain.md C14.
// They are not parameters: genesis cannot lower them, and there is no admin
// message that can either.
const (
	// HashLen is the length of a SHA-256 block hash, Merkle root and bundle content hash.
	HashLen = 32

	// MinArchiverAttestations is how many distinct archivers must attest the same
	// root before a range can be archived.
	MinArchiverAttestations = 3

	// MinReplicaDeals is how many distinct replica deal ids must be recorded
	// before a range can be archived.
	MinReplicaDeals = 3

	// MaxArchiversPerRange bounds attestation state. Quorum is 3; further
	// signatures are extra evidence, not a requirement.
	MaxArchiversPerRange = 64

	// MaxDealIDsPerRange bounds recorded deal ids. x/storage owns the deals;
	// this module records their ids and checks each is an active ARCHIVE deal.
	MaxDealIDsPerRange = 64

	// MaxBundleCIDLen is the longest bundle CID accepted.
	MaxBundleCIDLen = 128

	// ArchiveDealEpochs is how long the protocol ARCHIVE deal made for a range runs: ten years
	// of 24-hour epochs. The chain fixes it; an archiver cannot choose a longer or shorter
	// deal. A range whose deals end is renewed with a new MsgCreateArchiveDeal.
	ArchiveDealEpochs uint64 = 3650

	// MaxLiveDealsPerRange is how many live deals (recorded or still waiting for a provider) a
	// range may have. It is the replica quorum, so archiving a range never costs more than
	// MinReplicaDeals deals at a time.
	MaxLiveDealsPerRange = MinReplicaDeals

	// MaxNodeIDLen is the longest x/nodes node id accepted.
	MaxNodeIDLen = 128

	// MaxDealIDLen is the longest deal id accepted. Ids are x/storage deal
	// ids written in decimal.
	MaxDealIDLen = 128
)

const (
	// DefaultBlockIntervalSeconds is the block interval the default retention
	// window assumes. x/emission's floor is 14_400 blocks per day, which is
	// one block every 6 seconds.
	DefaultBlockIntervalSeconds int64 = 6

	// RetentionDays is the block-history window validators keep.
	RetentionDays int64 = 14

	secondsPerDay int64 = 24 * 60 * 60

	// DefaultBlocksIn14Days is 14 days of blocks at DefaultBlockIntervalSeconds.
	DefaultBlocksIn14Days int64 = RetentionDays * secondsPerDay / DefaultBlockIntervalSeconds

	// MinBlocksIn14Days is the smallest genesis window. A shorter window would
	// prune blocks younger than 14 days at the 6-second rate.
	MinBlocksIn14Days int64 = DefaultBlocksIn14Days

	// MaxBlocksIn14Days is 14 days of 1-second blocks, the fastest interval this
	// module accepts. A larger window would keep more than 14 days even then.
	MaxBlocksIn14Days int64 = RetentionDays * secondsPerDay
)
