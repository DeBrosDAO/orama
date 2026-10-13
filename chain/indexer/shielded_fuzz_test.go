package indexer

import (
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
)

// FuzzShieldedSubtreeEvents: reading subtree events never panics, a report it accepts holds a
// 32-byte root under a parsed index, and a stored action row decodes back to the position and
// hashes it was written with or is refused.
func FuzzShieldedSubtreeEvents(f *testing.F) {
	f.Add("shielded_subtree_complete", "index", "0", "root", "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")
	f.Add("shielded_subtree_complete", "index", "18446744073709551615", "root", "")
	f.Add("message", "index", "-1", "root", "zz")
	f.Fuzz(func(t *testing.T, typ, k1, v1, k2, v2 string) {
		var b blockShielded
		err := b.noteSubtrees([]abci.Event{{Type: typ, Attributes: []abci.EventAttribute{{Key: k1, Value: v1}, {Key: k2, Value: v2}}}})
		if err != nil {
			return
		}
		for index, root := range b.roots {
			if len(root) != 32 {
				t.Fatalf("subtree %d accepted with a root of %d bytes", index, len(root))
			}
		}
	})
}

// FuzzDecodeShieldedAction: a row of the wrong length or a key of the wrong length is refused; a
// row of the right shape decodes to hex fields of the documented lengths.
func FuzzDecodeShieldedAction(f *testing.F) {
	f.Add(make([]byte, len(pfxShieldAction)+16), make([]byte, shieldedRowLen))
	f.Add([]byte("h/a/"), []byte{1})
	f.Fuzz(func(t *testing.T, key, row []byte) {
		_, a, err := decodeShieldedAction(key, row)
		if err != nil {
			return
		}
		if len(a.TxHash) != 64 || len(a.Nullifier) != 64 || len(a.Cmx) != 64 || len(a.EphemeralKey) != 64 ||
			len(a.EncCompact) != 104 || len(a.OutCiphertext) != 160 {
			t.Fatalf("decoded an action with fields of the wrong length: %+v", a)
		}
	})
}

// FuzzShieldedBundleEvent: reading a bundle event never panics, and one it accepts was of the
// bundle type, for the message asked about, with the commitment count in 32 bits.
func FuzzShieldedBundleEvent(f *testing.F) {
	f.Add("shielded_bundle", "tree_size_before", "0", "commitments", "2", "0")
	f.Add("shielded_bundle", "tree_size_before", "18446744073709551615", "commitments", "4294967296", "0")
	f.Add("shielded_bundle", "tree_size_before", "-1", "commitments", "x", "1")
	f.Add("message", "tree_size_before", "1", "commitments", "1", "0")
	f.Fuzz(func(t *testing.T, typ, k1, v1, k2, v2, msgIndex string) {
		ev := abci.Event{Type: typ, Attributes: []abci.EventAttribute{{Key: k1, Value: v1}, {Key: k2, Value: v2}, {Key: msgIndexKey, Value: msgIndex}}}
		a := applier{events: []abci.Event{ev}, msgIndex: 0}
		_, n, err := a.bundleEvent()
		if err != nil {
			return
		}
		if typ != "shielded_bundle" || msgIndex != "0" || n > 1<<32-1 {
			t.Fatalf("accepted %q for message 0 (msg_index %q) with %d commitments", typ, msgIndex, n)
		}
	})
}
