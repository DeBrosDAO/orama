//go:build cgo && orchardffi

package orchard

import (
	"bytes"
	"errors"
	"testing"
)

// FuzzOrchardTreeAppend sends arbitrary frontier bytes and commitments across the FFI. Whatever the
// input, the call returns (no crash, no fault code), a refusal is ErrTreeRejected, an accepted
// frontier fits MaxFrontierLen, and appending in two steps equals appending at once, with the
// same root.
func FuzzOrchardTreeAppend(f *testing.F) {
	_, cmx := func() ([NodeLen]byte, [][NodeLen]byte) {
		var c [NodeLen]byte
		c[0] = 1
		return c, [][NodeLen]byte{c}
	}()
	tree := NewTree()
	good, _, err := tree.Append(nil, cmx)
	if err != nil {
		f.Fatal(err)
	}
	f.Add([]byte(nil), bytes.Repeat([]byte{1}, NodeLen), uint8(1))
	f.Add(good, bytes.Repeat([]byte{2}, 3*NodeLen), uint8(1))
	f.Add(good, bytes.Repeat([]byte{0xff}, NodeLen), uint8(0))
	f.Add(good[:len(good)-1], []byte{}, uint8(0))
	f.Add(bytes.Repeat([]byte{0xff}, MaxFrontierLen), bytes.Repeat([]byte{3}, NodeLen), uint8(0))
	f.Fuzz(func(t *testing.T, frontier, flat []byte, split uint8) {
		// Whatever the bytes, asking for a subtree root returns: a root, or ErrTreeRejected (a
		// frontier that does not decode or does not close a subtree).
		if _, err := tree.SubtreeRoot(frontier); err != nil && !errors.Is(err, ErrTreeRejected) {
			t.Fatalf("SubtreeRoot answered bytes with %v", err)
		}
		n := len(flat) / NodeLen
		if n > 8 {
			n = 8
		}
		cmx := make([][NodeLen]byte, n)
		for i := range cmx {
			copy(cmx[i][:], flat[i*NodeLen:])
		}
		all, root, err := tree.Append(frontier, cmx)
		if err != nil {
			if !errors.Is(err, ErrTreeRejected) {
				t.Fatalf("the FFI answered bytes with %v", err)
			}
			return
		}
		if len(all) > MaxFrontierLen {
			t.Fatalf("frontier of %d bytes", len(all))
		}
		cut := 0
		if n > 0 {
			cut = int(split) % (n + 1)
		}
		first, _, err := tree.Append(frontier, cmx[:cut])
		if err != nil {
			t.Fatalf("a prefix of an accepted append is refused: %v", err)
		}
		second, root2, err := tree.Append(first, cmx[cut:])
		if err != nil || !bytes.Equal(all, second) || root != root2 {
			t.Fatalf("two-step append differs from one step (%v)", err)
		}
	})
}
