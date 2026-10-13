package indexer

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strconv"

	abci "github.com/cometbft/cometbft/abci/types"
	gogoproto "github.com/cosmos/gogoproto/proto"

	"github.com/DeBrosOfficial/network/chain/x/shielded/bundle"
	shieldedtypes "github.com/DeBrosOfficial/network/chain/x/shielded/types"
)

// What the index keeps for a wallet that scans the shielded pool. For every committed shielded
// transaction (shield, shield-earnings, transfer, unshield) it records each Orchard action in the
// compact form a wallet trial-decrypts (ZIP-307), with the position its note commitment took in the
// note-commitment tree, and per block the tree size at the end of the block. It also records the
// root of every 2^16-leaf subtree of the tree, from the events the chain emits when one fills.
//
// Positions are read from the chain's events, never from its state: every shielded_bundle event
// carries the size the tree had before the bundle's append and the number of commitments appended
// (shieldedtypes.AttributeTreeSizeBefore, AttributeCommitments), so an action's position is the
// first plus its place in the bundle. The index checks that the bundles follow one another with no
// gap or overlap (flushShielded), which also proves it saw every append. A node that pruned its
// state serves the same index as one that kept it.

const (
	keyShieldSize   = "h/n"  // the tree size after the last indexed block with an action
	keySubtreeNext  = "h/x"  // the index of the next subtree root the index expects
	keySubtreesFrom = "h/f"  // the index of the first subtree root the index recorded
	pfxShieldAction = "h/a/" // height, tx index, action index in the tx → shieldedRow
	pfxShieldBlock  = "h/s/" // height → tree size at the end of the block
	pfxSubtree      = "h/r/" // subtree index → root (32), completing height (8)

	// shieldedRowLen is a stored action: position (8), transaction hash, nullifier, note
	// commitment and ephemeral key (32 each), the compact ciphertext and the out ciphertext.
	shieldedRowLen = 8 + 4*bundle.NodeLen + bundle.EncCompactLen + bundle.OutLen
	subtreeRowLen  = bundle.NodeLen + 8
)

func shieldedActionKey(height int64, tx, action uint32) []byte {
	return join([]byte(pfxShieldAction), be64(uint64(height)), be32(tx), be32(action))
}
func shieldedBlockKey(height int64) []byte { return join([]byte(pfxShieldBlock), be64(uint64(height))) }
func subtreeKey(index uint64) []byte       { return join([]byte(pfxSubtree), be64(index)) }

// ShieldedAction is one Orchard action as a wallet scans it. Every byte string is lowercase hex.
type ShieldedAction struct {
	// TxIndex is the transaction's place in its block, ActionIndex the action's place among the
	// actions of that transaction, and Position the leaf its note commitment took in the tree.
	TxIndex     uint32 `json:"tx_index"`
	ActionIndex uint32 `json:"action_index"`
	Position    uint64 `json:"position"`
	TxHash      string `json:"tx_hash"`
	Nullifier   string `json:"nullifier"`
	Cmx         string `json:"cmx"`
	// EphemeralKey, EncCompact (the first 52 bytes of the encrypted note) and OutCiphertext (80
	// bytes) are what a wallet needs to find a note sent to it and to recover one it sent.
	EphemeralKey  string `json:"ephemeral_key"`
	EncCompact    string `json:"enc_compact"`
	OutCiphertext string `json:"out_ciphertext"`
}

// ShieldedBlock is the actions of one block, in the order the chain appended their commitments.
// TreeSize is the number of commitments in the tree after the block.
type ShieldedBlock struct {
	Height   int64            `json:"height"`
	TreeSize uint64           `json:"tree_size"`
	Actions  []ShieldedAction `json:"actions"`
}

// SubtreeRoot is the root of a filled 2^16-leaf subtree and the block that filled it.
type SubtreeRoot struct {
	Index  uint64 `json:"index"`
	Root   string `json:"root"`
	Height int64  `json:"height"`
}

// pendingAction is an action found in a transaction of the block being indexed, with the leaf its
// note commitment took.
type pendingAction struct {
	tx, action uint32
	position   uint64
	hash       []byte
	a          bundle.CompactAction
}

// blockShielded is what the block being indexed has put in the tree: its actions in order, and the
// subtree roots the chain reported while it did.
type blockShielded struct {
	actions []pendingAction
	roots   map[uint64][]byte
}

func (b *blockShielded) reset() { b.actions, b.roots = nil, nil }

// add records the actions of one bundle in the transaction at tx, whose first commitment took the
// leaf at before.
func (b *blockShielded) add(tx uint32, hash []byte, before uint64, actions []bundle.CompactAction) {
	next := uint32(0)
	for i := len(b.actions) - 1; i >= 0 && b.actions[i].tx == tx; i-- {
		next++
	}
	for i, a := range actions {
		b.actions = append(b.actions, pendingAction{tx: tx, action: next + uint32(i), position: before + uint64(i), hash: hash, a: a})
	}
}

// noteSubtrees records the subtree roots a successful transaction's events report.
func (b *blockShielded) noteSubtrees(events []abci.Event) error {
	for _, e := range events {
		if e.Type != shieldedtypes.EventTypeSubtreeComplete {
			continue
		}
		var index uint64
		var root []byte
		for _, at := range e.Attributes {
			var err error
			switch at.Key {
			case shieldedtypes.AttributeSubtreeIndex:
				index, err = strconv.ParseUint(at.Value, 10, 64)
			case shieldedtypes.AttributeSubtreeRoot:
				root, err = hex.DecodeString(at.Value)
			}
			if err != nil {
				return fmt.Errorf("%s event attribute %s=%q: %w", e.Type, at.Key, at.Value, err)
			}
		}
		if len(root) != bundle.NodeLen {
			return fmt.Errorf("%s event carries a root of %d bytes, want %d", e.Type, len(root), bundle.NodeLen)
		}
		if b.roots == nil {
			b.roots = map[uint64][]byte{}
		}
		if _, dup := b.roots[index]; dup {
			return fmt.Errorf("two %s events for subtree %d in one block", e.Type, index)
		}
		b.roots[index] = root
	}
	return nil
}

// bundleOf is the bundle bytes a shielded message carries, or nil for any other message.
func bundleOf(msg gogoproto.Message) []byte {
	switch m := msg.(type) {
	case *shieldedtypes.MsgShield:
		return m.Bundle
	case *shieldedtypes.MsgShieldEarnings:
		return m.Bundle
	case *shieldedtypes.MsgShieldedTransfer:
		return m.Bundle
	case *shieldedtypes.MsgUnshield:
		return m.Bundle
	}
	return nil
}

// bundleEvent returns the tree size before the append of the bundle the current message carried
// and the number of commitments it appended, from the shielded_bundle event the message emitted.
// Every committed shielded message emits exactly one, so none, or two, is an index that does not
// understand the chain.
func (a applier) bundleEvent() (before, n uint64, err error) {
	var found map[string]string
	for _, e := range a.events {
		if e.Type != shieldedtypes.EventTypeBundle || attrOf(e, msgIndexKey) != strconv.Itoa(a.msgIndex) {
			continue
		}
		if found != nil {
			return 0, 0, fmt.Errorf("message %d emitted two %s events", a.msgIndex, e.Type)
		}
		found = map[string]string{}
		for _, at := range e.Attributes {
			found[at.Key] = at.Value
		}
	}
	if found == nil {
		return 0, 0, fmt.Errorf("message %d is a committed shielded message but emitted no %s event", a.msgIndex, shieldedtypes.EventTypeBundle)
	}
	if before, err = eventUint(found, shieldedtypes.EventTypeBundle, shieldedtypes.AttributeTreeSizeBefore, 64); err != nil {
		return 0, 0, err
	}
	n, err = eventUint(found, shieldedtypes.EventTypeBundle, shieldedtypes.AttributeCommitments, 32)
	return before, n, err
}

// applyShielded notes the actions of a committed shielded message. The chain accepted the bundle,
// so one the framing code refuses means the index and the chain disagree about the format, and so
// does an event that counts other commitments than the bundle holds actions.
func applyShielded(a applier, msg, _ gogoproto.Message) error {
	actions, err := bundle.CompactActions(bundleOf(msg))
	if err != nil {
		return fmt.Errorf("shielded bundle the chain accepted does not parse: %w", err)
	}
	hash, err := hex.DecodeString(a.tx)
	if err != nil {
		return fmt.Errorf("transaction hash %q: %w", a.tx, err)
	}
	before, n, err := a.bundleEvent()
	if err != nil {
		return err
	}
	if n != uint64(len(actions)) {
		return fmt.Errorf("transaction %s: the chain appended %d commitments for a bundle of %d actions", a.tx, n, len(actions))
	}
	a.shielded.add(a.txIndex, hash, before, actions)
	return nil
}

// flushShielded writes the block's actions with their positions, the block's end tree size and the
// subtree roots it completed. It does nothing for a block with neither.
func (f *Follower) flushShielded(w *writer, height int64) error {
	b := &f.shielded
	if len(b.actions) == 0 {
		if len(b.roots) > 0 {
			return fmt.Errorf("the chain reported subtree roots in a block with no shielded action")
		}
		return nil
	}
	base, err := f.checkContinuity(w, height)
	if err != nil {
		return err
	}
	for _, p := range b.actions {
		if err := w.set(shieldedActionKey(height, p.tx, p.action), shieldedRow(p.position, p)); err != nil {
			return err
		}
	}
	end := base + uint64(len(b.actions))
	if err := w.set([]byte(keyShieldSize), be64(end)); err != nil {
		return err
	}
	if err := w.set(shieldedBlockKey(height), be64(end)); err != nil {
		return err
	}
	return w.recordSubtrees(height, base, end, b.roots)
}

// checkContinuity returns the leaf the block's first action took, after checking that the block's
// actions take consecutive leaves and that the first follows the last action the index holds: a
// gap means a shielded append the index did not see (an append by something other than a bundle, or
// a transaction it skipped), an overlap that the chain's events disagree with themselves. The first
// block with an action the index sees sets where its positions begin, as it may have started inside
// the chain's history.
func (f *Follower) checkContinuity(w *writer, height int64) (uint64, error) {
	next, have, err := getUint(w.b, []byte(keyShieldSize))
	if err != nil {
		return 0, err
	}
	base := f.shielded.actions[0].position
	if have && base != next {
		return 0, fmt.Errorf("the first shielded action of block %d took leaf %d but the index holds %d leaves", height, base, next)
	}
	for i, p := range f.shielded.actions {
		if p.position != base+uint64(i) {
			return 0, fmt.Errorf("shielded action %d of block %d (transaction %d) took leaf %d, want %d", i, height, p.tx, p.position, base+uint64(i))
		}
	}
	return base, nil
}

// recordSubtrees stores the roots of the subtrees the block filled, the leaves base to end. The
// chain reports one per subtree it fills; the first one the index sees sets where its roots begin,
// and from then on a subtree filled without a root is an error, as a settle without its event is
// (chain_events.go): a chain that emitted the events once keeps emitting them.
func (w *writer) recordSubtrees(height int64, base, end uint64, roots map[uint64][]byte) error {
	first, last := base/shieldedtypes.SubtreeLeaves, end/shieldedtypes.SubtreeLeaves // filled: first..last-1
	for index := range roots {
		if index < first || index >= last {
			return fmt.Errorf("the chain reported subtree %d filled in a block that took the tree from %d to %d leaves", index, base, end)
		}
	}
	from, started, err := getUint(w.b, []byte(keySubtreesFrom))
	if err != nil {
		return err
	}
	for index := first; index < last; index++ {
		root, reported := roots[index]
		if !reported {
			if started && index >= from {
				return fmt.Errorf("subtree %d filled at height %d with no %s event, but this chain has reported them since subtree %d; this indexer does not understand the chain's events", index, height, shieldedtypes.EventTypeSubtreeComplete, from)
			}
			continue
		}
		next, have, err := getUint(w.b, []byte(keySubtreeNext))
		if err != nil {
			return err
		}
		if !have {
			if err := w.set([]byte(keySubtreesFrom), be64(index)); err != nil {
				return err
			}
			started, from = true, index
		} else if next != index {
			return fmt.Errorf("subtree %d filled at height %d but the index expected %d", index, height, next)
		}
		if err := w.set(subtreeKey(index), join(root, be64(uint64(height)))); err != nil {
			return err
		}
		if err := w.set([]byte(keySubtreeNext), be64(index+1)); err != nil {
			return err
		}
	}
	return nil
}

// shieldedRow encodes an action as stored.
func shieldedRow(position uint64, p pendingAction) []byte {
	row := make([]byte, 0, shieldedRowLen)
	row = binary.BigEndian.AppendUint64(row, position)
	row = append(row, p.hash...)
	row = append(row, p.a.Nullifier[:]...)
	row = append(row, p.a.Cmx[:]...)
	row = append(row, p.a.EphemeralKey[:]...)
	row = append(row, p.a.EncCompact[:]...)
	row = append(row, p.a.OutCipher[:]...)
	return row
}

// decodeShieldedAction reads a stored action at the place its key names.
func decodeShieldedAction(key, row []byte) (int64, ShieldedAction, error) {
	prefix := len(pfxShieldAction)
	if len(row) != shieldedRowLen || len(key) != prefix+8+4+4 {
		return 0, ShieldedAction{}, fmt.Errorf("shielded action row %x holds %d bytes under a %d-byte key, want %d under %d", key, len(row), len(key), shieldedRowLen, prefix+16)
	}
	at := 8
	next := func(n int) string {
		s := hex.EncodeToString(row[at : at+n])
		at += n
		return s
	}
	a := ShieldedAction{
		TxIndex:     binary.BigEndian.Uint32(key[prefix+8:]),
		ActionIndex: binary.BigEndian.Uint32(key[prefix+12:]),
		Position:    binary.BigEndian.Uint64(row),
	}
	a.TxHash = next(bundle.NodeLen)
	a.Nullifier = next(bundle.NodeLen)
	a.Cmx = next(bundle.NodeLen)
	a.EphemeralKey = next(bundle.NodeLen)
	a.EncCompact = next(bundle.EncCompactLen)
	a.OutCiphertext = next(bundle.OutLen)
	return int64(binary.BigEndian.Uint64(key[prefix:])), a, nil
}
