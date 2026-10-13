package indexer

import (
	"encoding/hex"
	"fmt"
	"strconv"

	abci "github.com/cometbft/cometbft/abci/types"

	cnfttypes "github.com/DeBrosOfficial/network/chain/x/cnft/types"
	markettypes "github.com/DeBrosOfficial/network/chain/x/market/types"
)

// msgIndexKey is the attribute the SDK appends to every event a message emitted: the message's
// position in its transaction. A transaction's events are all in one list, so it is what tells two
// settles in one transaction apart.
const msgIndexKey = "msg_index"

// Settle and compress consume state that no longer exists afterwards (the listing, the bid, the
// decompressed record), and the message does not carry it. The chain reports it in a market_settle
// and a cnft_compress event, so the index reads it from the transaction itself and never from the
// state before the block, which a node that pruned that state cannot answer.
//
// A transaction cannot say by itself whether its binary was one that emits the events, so the index
// records the first height at which it saw either event (keyEventsFrom) and applies this rule to a
// settle or a compress that carries no event:
//
//   - Before the first event the index has seen, the transaction ran on a binary that did not emit
//     it: on a chain that moves to an emitting binary, the first settle or compress after the move
//     carries its event, so every one before it is older. It is read from the state before the
//     block, as it always was; a node that pruned that state stops the follower with state_pruned,
//     which is the limit of an index that starts inside the history of a binary that did not emit
//     the events.
//   - From that height on, a settle or compress without its event is an error that stops the
//     follower. The chain only moves forward to a binary that emits them; an absent event after
//     that is a decoder or a chain the index does not understand, and reading state to cover for
//     it would hide it.
//
// The rule depends only on the chain's own blocks, in order, so every indexer of the chain decides
// the same for every block it reads, whatever height it started at. Both events arrive in one
// release, so one height serves both.

// msgEvent returns the attributes of the event of typ the current message of the transaction
// emitted, or nil when the transaction carries none for it and the index has not yet seen the chain
// emit these events (see the rule above). It is an error to find the event twice, or to find none
// after the chain has started emitting them.
func (a applier) msgEvent(typ string) (map[string]string, error) {
	var found map[string]string
	for _, e := range a.events {
		if e.Type != typ || attrOf(e, msgIndexKey) != strconv.Itoa(a.msgIndex) {
			continue
		}
		if found != nil {
			return nil, fmt.Errorf("message %d emitted two %s events", a.msgIndex, typ)
		}
		found = map[string]string{}
		for _, at := range e.Attributes {
			found[at.Key] = at.Value
		}
	}
	from, seen, err := a.w.eventsFrom(keyEventsFrom)
	if err != nil {
		return nil, err
	}
	if found != nil {
		if !seen {
			if err := a.w.setEventsFrom(keyEventsFrom, a.height); err != nil {
				return nil, err
			}
		}
		return found, nil
	}
	if seen {
		return nil, fmt.Errorf("no %s event, but this chain has emitted it since height %d; this indexer does not understand the chain's events", typ, from)
	}
	return nil, nil
}

func attrOf(e abci.Event, key string) string {
	for _, at := range e.Attributes {
		if at.Key == key {
			return at.Value
		}
	}
	return ""
}

// eventUint reads an unsigned decimal attribute of at most bits bits.
func eventUint(ev map[string]string, typ, key string, bits int) (uint64, error) {
	v, ok := ev[key]
	if !ok {
		return 0, fmt.Errorf("%s event has no %s", typ, key)
	}
	n, err := strconv.ParseUint(v, 10, bits)
	if err != nil {
		return 0, fmt.Errorf("%s event %s %q: %w", typ, key, v, err)
	}
	return n, nil
}

func eventHex(ev map[string]string, typ, key string) ([]byte, error) {
	b, err := hex.DecodeString(ev[key])
	if err != nil {
		return nil, fmt.Errorf("%s event %s %q: %w", typ, key, ev[key], err)
	}
	return b, nil
}

// settleEvent is the market_settle event of a settle.
type settleEvent struct {
	listingID, bidID, treeID uint64
	leafIndex                uint32
	assetID                  []byte
	buyer                    string
}

// settleFromEvent decodes the event and checks it against the message that produced it: the
// listing, bid, asset and leaf index the settle named must be the ones the event reports, and a
// buy-now settle's buyer is its signer.
func settleFromEvent(ev map[string]string, m *markettypes.MsgSettle) (settleEvent, error) {
	const typ = markettypes.EventTypeSettle
	var s settleEvent
	var err error
	if s.listingID, err = eventUint(ev, typ, markettypes.AttributeKeyListingID, 64); err != nil {
		return s, err
	}
	if s.bidID, err = eventUint(ev, typ, markettypes.AttributeKeyBidID, 64); err != nil {
		return s, err
	}
	if s.treeID, err = eventUint(ev, typ, markettypes.AttributeKeyTreeID, 64); err != nil {
		return s, err
	}
	index, err := eventUint(ev, typ, markettypes.AttributeKeyLeafIndex, 32)
	if err != nil {
		return s, err
	}
	s.leafIndex = uint32(index)
	if s.assetID, err = eventHex(ev, typ, markettypes.AttributeKeyAssetID); err != nil {
		return s, err
	}
	if s.buyer, _, err = canonicalAccount(ev[markettypes.AttributeKeyBuyer]); err != nil {
		return s, fmt.Errorf("%s event buyer: %w", typ, err)
	}
	if s.listingID != m.ListingId || s.bidID != m.BidId || s.leafIndex != m.Proof.Index || hex.EncodeToString(s.assetID) != hex.EncodeToString(m.Leaf.AssetId) {
		return s, fmt.Errorf("%s event (listing %d, bid %d, leaf %d, asset %x) is not the settle it came with (listing %d, bid %d, leaf %d, asset %x)",
			typ, s.listingID, s.bidID, s.leafIndex, s.assetID, m.ListingId, m.BidId, m.Proof.Index, m.Leaf.AssetId)
	}
	if m.BidId == 0 {
		signer, _, err := canonicalAccount(m.Signer)
		if err != nil {
			return s, fmt.Errorf("settle signer: %w", err)
		}
		if signer != s.buyer {
			return s, fmt.Errorf("%s event buyer %s is not the signer %s of a buy-now settle", typ, s.buyer, signer)
		}
	}
	return s, nil
}

// compressEvent is the cnft_compress event of a compress: the decompressed record it consumed and
// the leaf it appended.
type compressEvent struct {
	prev      cnfttypes.DecompressedAsset
	treeID    uint64
	leafIndex uint32
}

// compressFromEvent decodes the event and checks it against the message and the response it came
// with: the asset, and the tree and leaf the response names.
func compressFromEvent(ev map[string]string, m *cnfttypes.MsgCompress, r *cnfttypes.MsgCompressResponse) (compressEvent, error) {
	const typ = cnfttypes.EventTypeCompress
	var c compressEvent
	id, err := eventHex(ev, typ, cnfttypes.AttributeKeyAssetID)
	if err != nil {
		return c, err
	}
	creatorHash, err := eventHex(ev, typ, cnfttypes.AttributeKeyCreatorHash)
	if err != nil {
		return c, err
	}
	hashID, err := eventUint(ev, typ, cnfttypes.AttributeKeyHashID, 32)
	if err != nil {
		return c, err
	}
	if c.treeID, err = eventUint(ev, typ, cnfttypes.AttributeKeyTreeID, 64); err != nil {
		return c, err
	}
	index, err := eventUint(ev, typ, cnfttypes.AttributeKeyLeafIndex, 32)
	if err != nil {
		return c, err
	}
	c.leafIndex = uint32(index)
	c.prev = cnfttypes.DecompressedAsset{
		AssetId: id, Delegate: ev[cnfttypes.AttributeKeyDelegate], MetadataCid: ev[cnfttypes.AttributeKeyMetadataCID],
		CreatorHash: creatorHash, HashId: uint32(hashID),
	}
	if hex.EncodeToString(id) != hex.EncodeToString(m.AssetId) || c.treeID != r.TreeId || c.leafIndex != r.LeafIndex {
		return c, fmt.Errorf("%s event (asset %x, tree %d, leaf %d) is not the compress it came with (asset %x, tree %d, leaf %d)",
			typ, id, c.treeID, c.leafIndex, m.AssetId, r.TreeId, r.LeafIndex)
	}
	return c, nil
}
