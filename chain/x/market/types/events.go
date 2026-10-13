package types

// EventTypeSettle is emitted once by each successful MsgSettle. It carries what the settle consumed
// from state (the listing it deleted and the bid it accepted), which the message and its response
// do not: the message names the listing and bid by id, and the listing's tree and the bid's bidder
// exist only in state that the settle then deletes. An indexer that did not watch the listing being
// created would otherwise have to read that state back at the previous height.
//
// A settle that fails emits nothing: the event is added at the end of the handler, and a failed
// message's events are dropped with its state.
const EventTypeSettle = "market_settle"

const (
	// AttributeKeyListingID is the settled listing's id (decimal).
	AttributeKeyListingID = "listing_id"
	// AttributeKeyBidID is the accepted bid's id (decimal), 0 for a buy-now settle.
	AttributeKeyBidID = "bid_id"
	// AttributeKeyTreeID is the id (decimal) of the tree the listed leaf was in.
	AttributeKeyTreeID = "tree_id"
	// AttributeKeyLeafIndex is the listed leaf's index in that tree (decimal).
	AttributeKeyLeafIndex = "leaf_index"
	// AttributeKeyAssetID is the listed asset's id, lowercase hex.
	AttributeKeyAssetID = "asset_id"
	// AttributeKeyBuyer is the account the leaf moved to: the signer for a buy-now settle, the
	// bidder for an accepted bid.
	AttributeKeyBuyer = "buyer"
)
