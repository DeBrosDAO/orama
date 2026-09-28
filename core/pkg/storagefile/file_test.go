package storagefile

import (
	"bytes"
	"errors"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/pieceroot"
)

func TestPrepareOpenRoundTrip(t *testing.T) {
	seed := bytes.Repeat([]byte{1}, 32)
	repair := bytes.Repeat([]byte{2}, 32)
	nonce := bytes.Repeat([]byte{3}, DealNonceLen)
	plain := []byte("a private file")
	slots, err := Prepare(seed, repair, nonce, 3, plain)
	if err != nil {
		t.Fatal(err)
	}
	if len(slots) != 3 {
		t.Fatalf("slots %d", len(slots))
	}
	seen := map[string]struct{}{}
	for i, slot := range slots {
		if slot.Index != uint32(i) {
			t.Fatalf("index %d", slot.Index)
		}
		if _, ok := seen[string(slot.Bytes)]; ok {
			t.Fatal("two slots share ciphertext")
		}
		seen[string(slot.Bytes)] = struct{}{}
		c, err := pieceroot.Commit(slot.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(c.Root, slot.Root) || c.RealLeafCount != slot.RealLeafCount {
			t.Fatal("root drifted from the piece commitment")
		}
		got, err := Open(seed, repair, nonce, slot.Index, slot.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, plain) {
			t.Fatalf("open %q", got)
		}
	}
	if _, err := Open(bytes.Repeat([]byte{9}, 32), repair, nonce, 0, slots[0].Bytes); !errors.Is(err, ErrNotForKey) {
		t.Fatalf("wrong seed %v", err)
	}
	if _, err := Open(seed, bytes.Repeat([]byte{9}, 32), nonce, 0, slots[0].Bytes); !errors.Is(err, ErrNotForKey) {
		t.Fatalf("wrong repair %v", err)
	}
	if _, err := Open(seed, repair, nonce, 1, slots[0].Bytes); !errors.Is(err, ErrNotForKey) {
		t.Fatalf("wrong slot %v", err)
	}
	empty, err := Prepare(seed, repair, nonce, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Open(seed, repair, nonce, 0, empty[0].Bytes)
	if err != nil || len(got) != 0 {
		t.Fatalf("empty %q %v", got, err)
	}
	if _, err := Prepare(seed[:16], repair, nonce, 1, plain); err == nil {
		t.Fatal("short seed was accepted")
	}
}
