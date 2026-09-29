package types

const (
	// EventTypeAttest is emitted when an archiver attests a range.
	EventTypeAttest = "archive_attest"
	// EventTypeAttachReplicas is emitted when deal ids are recorded.
	EventTypeAttachReplicas = "archive_attach_replicas"
	// EventTypeCreateArchiveDeal is emitted when an ARCHIVE deal is made for a range.
	EventTypeCreateArchiveDeal = "archive_create_deal"
	// EventTypeArchived is emitted once, when a range first meets quorum.
	EventTypeArchived = "archive_archived"

	// AttributeKeyArchiver is the attesting or attaching signer.
	AttributeKeyArchiver = "archiver"
	// AttributeKeyStartHeight is the inclusive start height.
	AttributeKeyStartHeight = "start_height"
	// AttributeKeyEndHeight is the inclusive end height.
	AttributeKeyEndHeight = "end_height"
	// AttributeKeyBundleCID is the pinned bundle CID.
	AttributeKeyBundleCID = "bundle_cid"
	// AttributeKeyDealID is the x/storage deal id, in decimal.
	AttributeKeyDealID = "deal_id"
	// AttributeKeyArchived is "true" once the range is archived.
	AttributeKeyArchived = "archived"
)
