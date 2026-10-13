package bundle

import (
	"bytes"
	"testing"
)

// FuzzCompactActions: CompactActions accepts exactly what Parse accepts, reads each field at its
// offset in the serialized action, and an accepted bundle is exactly Len(actions) bytes long.
func FuzzCompactActions(f *testing.F) {
	seedBundles(f)
	f.Fuzz(func(t *testing.T, b []byte) {
		parsed, perr := Parse(b)
		got, err := CompactActions(b)
		if (perr == nil) != (err == nil) {
			t.Fatalf("Parse: %v, CompactActions: %v", perr, err)
		}
		if err != nil {
			return
		}
		if len(got) != parsed.Actions || len(b) != Len(parsed.Actions) {
			t.Fatalf("%d compact actions, %d parsed, %d bytes, Len %d", len(got), parsed.Actions, len(b), Len(parsed.Actions))
		}
		head := len(compactSize(uint64(parsed.Actions)))
		for i, a := range got {
			at := head + i*ActionLen
			if a.Nullifier != parsed.Nullifiers[i] || a.Cmx != parsed.Commitments[i] ||
				!bytes.Equal(a.EphemeralKey[:], b[at+128:at+160]) ||
				!bytes.Equal(a.EncCompact[:], b[at+160:at+160+EncCompactLen]) ||
				!bytes.Equal(a.OutCipher[:], b[at+740:at+820]) {
				t.Fatalf("action %d is not the bytes at its offsets", i)
			}
		}
	})
}
