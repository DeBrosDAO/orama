package snapshot

import (
	"encoding/binary"
	"testing"

	dbm "github.com/cosmos/cosmos-db"

	"github.com/DeBrosOfficial/network/chain/x/shielded/bundle"
	"github.com/DeBrosOfficial/network/chain/x/shielded/nullifier"
)

// FuzzSnapshotRestore feeds peer-supplied payloads to RestoreExtension. The committed state is
// the honest one for the records as the payloads carry them (the accumulator and count of the
// whole stream), so the fold check cannot be what refuses them: the rest of the importer must.
// Properties: no panic; a refused restore leaves the store empty for the next attempt; an
// accepted one holds exactly the stream's records, in order, none above the snapshot height.
func FuzzSnapshotRestore(f *testing.F) {
	rec := func(h uint64, n int) []byte {
		out := binary.BigEndian.AppendUint64(nil, h)
		id := nf(n)
		return append(out, id[:]...)
	}
	f.Add(append(rec(1, 1), rec(2, 2)...), uint64(5), true)
	f.Add(append(rec(1, 1), rec(1, 1)...), uint64(5), true)
	f.Add(append(rec(3, 1), rec(2, 2)...), uint64(5), true)
	f.Add(rec(9, 1), uint64(5), false)
	f.Add(rec(^uint64(0), 1), uint64(5), true)
	f.Add([]byte{1, 2, 3}, uint64(5), true)
	f.Fuzz(func(t *testing.T, stream []byte, height uint64, split bool) {
		height %= 1 << 40
		store := nullifier.NewStore(dbm.NewMemDB())

		var records []struct {
			h  int64
			nf [bundle.NodeLen]byte
		}
		var acc [bundle.NodeLen]byte
		whole := len(stream)%recordLen == 0
		for p := stream; whole && len(p) >= recordLen; p = p[recordLen:] {
			var n [bundle.NodeLen]byte
			copy(n[:], p[8:recordLen])
			records = append(records, struct {
				h  int64
				nf [bundle.NodeLen]byte
			}{int64(binary.BigEndian.Uint64(p)), n})
			acc = nullifier.Fold(acc, n)
		}
		ext := New(store, func(uint64) ([bundle.NodeLen]byte, uint64, error) { return acc, uint64(len(records)), nil })

		var payloads [][]byte
		if split && len(stream) > recordLen {
			cut := (len(stream) / 2 / recordLen) * recordLen
			if cut == 0 {
				cut = recordLen
			}
			payloads = [][]byte{stream[:cut], stream[cut:]}
		} else {
			payloads = [][]byte{stream}
		}
		err := ext.RestoreExtension(height, Format, reader(payloads))
		empty, emptyErr := store.Empty()
		if emptyErr != nil {
			t.Fatal(emptyErr)
		}
		if err != nil {
			if !empty {
				t.Fatalf("a refused restore left records behind: %v", err)
			}
			return
		}
		var got int
		last := int64(-1 << 62)
		walkErr := store.Walk(func(n [bundle.NodeLen]byte, h int64) error {
			if got >= len(records) || records[got].nf != n || records[got].h != h {
				t.Fatalf("record %d of the store is not record %d of the stream", got, got)
			}
			if h < 0 || h > int64(height) || h < last {
				t.Fatalf("restored a record at height %d (snapshot height %d, previous %d)", h, height, last)
			}
			last = h
			got++
			return nil
		})
		if walkErr != nil || got != len(records) {
			t.Fatalf("store holds %d of %d records: %v", got, len(records), walkErr)
		}
	})
}
