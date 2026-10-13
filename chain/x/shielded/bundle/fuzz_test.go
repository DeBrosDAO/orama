package bundle

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/DeBrosOfficial/network/chain/x/shielded/verify"
)

// compactSize is the canonical Bitcoin CompactSize encoding, the inverse of ReadCompactSize.
func compactSize(v uint64) []byte {
	switch {
	case v < 253:
		return []byte{byte(v)}
	case v < 1<<16:
		return binary.LittleEndian.AppendUint16([]byte{253}, uint16(v))
	case v < 1<<32:
		return binary.LittleEndian.AppendUint32([]byte{254}, uint32(v))
	}
	return binary.LittleEndian.AppendUint64([]byte{255}, v)
}

// seedBundles adds the real Ironwood vectors the verifiers are tested with, and the framing edges.
func seedBundles(f *testing.F) {
	for _, name := range []string{"ironwood-1-action", "ironwood-2-action", "ironwood-transfer", "ironwood-unshield"} {
		raw, err := os.ReadFile(filepath.Join("..", "orchardffi", "testdata", name+".bundle"))
		if err != nil {
			f.Fatal(err)
		}
		f.Add(raw)
	}
	f.Add([]byte{})
	f.Add([]byte{1})
	f.Add([]byte{253, 1, 0})
	f.Add(bytes.Repeat([]byte{255}, 9))
}

// FuzzBundleParse: Parse never panics, and an accepted bundle is exactly the canonical layout
// (the counts agree, the body length is the formula's, the fields are read from the bytes the
// signatures cover). A refused one is always a verify.ErrTampered, the class the verifiers use.
func FuzzBundleParse(f *testing.F) {
	seedBundles(f)
	f.Fuzz(func(t *testing.T, b []byte) {
		got, err := Parse(b)
		if err != nil {
			if !errors.Is(err, verify.ErrTampered) {
				t.Fatalf("refusal is not an ErrTampered: %v", err)
			}
			return
		}
		n := got.Actions
		if n < 1 || len(got.Nullifiers) != n || len(got.Commitments) != n {
			t.Fatalf("%d actions, %d nullifiers, %d commitments", n, len(got.Nullifiers), len(got.Commitments))
		}
		prefix, err := EffectingData(b)
		if err != nil {
			t.Fatalf("Parse accepted what EffectingData refuses: %v", err)
		}
		head := compactSize(uint64(n))
		want := len(head) + n*ActionLen + HeaderLen + len(compactSize(uint64(ProofLen(n)))) + ProofLen(n) + (n+1)*SigLen
		if len(b) != want || len(prefix) != len(head)+n*ActionLen+HeaderLen || !bytes.HasPrefix(b, head) {
			t.Fatalf("accepted a %d byte bundle of %d actions, canonical size is %d", len(b), n, want)
		}
		for i := 0; i < n; i++ {
			at := len(head) + i*ActionLen
			if !bytes.Equal(got.Nullifiers[i][:], b[at+nullifierAt:at+nullifierAt+NodeLen]) ||
				!bytes.Equal(got.Commitments[i][:], b[at+cmxAt:at+cmxAt+NodeLen]) {
				t.Fatalf("action %d fields are not the bytes at their offsets", i)
			}
		}
		hdr := len(head) + n*ActionLen
		if got.Flags != b[hdr] || got.ValueBalance != int64(binary.LittleEndian.Uint64(b[hdr+1:])) ||
			!bytes.Equal(got.Anchor[:], b[hdr+9:hdr+9+NodeLen]) {
			t.Fatal("header fields are not the bytes at their offsets")
		}
		again, err := Parse(bytes.Clone(b))
		if err != nil || again.Actions != n || again.ValueBalance != got.ValueBalance {
			t.Fatalf("Parse is not deterministic: %v", err)
		}
	})
}

// FuzzReadCompactSize: a value ReadCompactSize accepts re-encodes to the very same bytes (so no
// two encodings of one number are both accepted), and it never reads past the slice.
func FuzzReadCompactSize(f *testing.F) {
	for _, s := range [][]byte{{}, {0}, {252}, {253, 253, 0}, {253, 252, 0}, {254, 0, 0, 1, 0}, {255, 0, 0, 0, 0, 1, 0, 0, 0}, {255, 255, 255, 255, 255, 255, 255, 255, 255}} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		v, used, ok := ReadCompactSize(b)
		if !ok {
			return
		}
		if used < 1 || used > len(b) || !bytes.Equal(compactSize(v), b[:used]) {
			t.Fatalf("accepted %x as %d using %d bytes, canonical form is %x", b, v, used, compactSize(v))
		}
	})
}
