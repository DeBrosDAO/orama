package types

// EventTypeCompress is emitted once by each successful MsgCompress. It carries the decompressed
// asset record the compress consumed and deleted, with the leaf the asset moved to: the message
// names only the asset id, and the rest of the record (delegate, metadata, creator hash, hash id)
// exists only in state that the compress removes. An indexer that did not watch the asset being
// decompressed would otherwise have to read that state back at the previous height.
//
// A compress that fails emits nothing: the event is added at the end of the handler, and a failed
// message's events are dropped with its state.
const EventTypeCompress = "cnft_compress"

const (
	// AttributeKeyAssetID is the compressed asset's id, lowercase hex.
	AttributeKeyAssetID = "asset_id"
	// AttributeKeyDelegate is the record's delegate, empty when it had none.
	AttributeKeyDelegate = "delegate"
	// AttributeKeyMetadataCID is the record's metadata CID.
	AttributeKeyMetadataCID = "metadata_cid"
	// AttributeKeyCreatorHash is the record's creator hash, lowercase hex.
	AttributeKeyCreatorHash = "creator_hash"
	// AttributeKeyHashID is the record's leaf hash function id (decimal).
	AttributeKeyHashID = "hash_id"
	// AttributeKeyTreeID is the id (decimal) of the tree the new leaf was appended to.
	AttributeKeyTreeID = "tree_id"
	// AttributeKeyLeafIndex is the new leaf's index in that tree (decimal).
	AttributeKeyLeafIndex = "leaf_index"
)
