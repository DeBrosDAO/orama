package nullifier

import (
	"sort"
	"testing"

	dbm "github.com/cosmos/cosmos-db"
)

// FuzzNullifierStore drives the store with a byte-coded sequence of Commit, Spent and rollback
// operations and compares every answer with a plain model: a record at height h is visible below
// asOf when h < asOf, a Commit at h replaces everything at or above h, a Commit that errors
// changes nothing, and Walk is height order then insertion order.
func FuzzNullifierStore(f *testing.F) {
	f.Add([]byte{0, 1, 2, 3, 0, 2, 4, 5, 1, 3, 0})
	f.Add([]byte{0, 1, 1, 1, 0, 1, 1})
	f.Add([]byte{0, 3, 9, 8, 0, 2, 9, 0, 1, 2, 2})
	f.Fuzz(func(t *testing.T, script []byte) {
		s := NewStore(dbm.NewMemDB())
		model := map[int64][][nodeLen]byte{}
		spentIn := func(nf [nodeLen]byte, below int64) bool {
			for h, list := range model {
				for _, have := range list {
					if have == nf && h < below {
						return true
					}
				}
			}
			return false
		}
		nfOf := func(b byte) [nodeLen]byte { return [nodeLen]byte{b % 11, 7} }
		for len(script) > 0 {
			op := script[0] % 3
			script = script[1:]
			if len(script) == 0 {
				break
			}
			height := int64(script[0]%6) + 1
			script = script[1:]
			switch op {
			case 0:
				var block [][nodeLen]byte
				for len(script) > 0 && script[0]%5 != 0 {
					block = append(block, nfOf(script[0]))
					script = script[1:]
				}
				if len(script) > 0 {
					script = script[1:]
				}
				want := commitModel(model, height, block, spentIn)
				err := s.Commit(height, block)
				if (err == nil) != want {
					t.Fatalf("Commit(%d, %d records): store error %v, model accepts %v", height, len(block), err, want)
				}
			case 1:
				nf := nfOf(byte(height))
				got, err := s.Spent(nf, height)
				if err != nil || got != spentIn(nf, height) {
					t.Fatalf("Spent(%x, %d) = %v, %v; model %v", nf, height, got, err, spentIn(nf, height))
				}
			default:
				if err := compareWalk(s, model); err != "" {
					t.Fatal(err)
				}
			}
		}
		if err := compareWalk(s, model); err != "" {
			t.Fatal(err)
		}
	})
}

// commitModel applies Commit to the model and reports whether the store must accept it.
func commitModel(model map[int64][][nodeLen]byte, height int64, block [][nodeLen]byte, spentIn func([nodeLen]byte, int64) bool) bool {
	seen := map[[nodeLen]byte]bool{}
	for _, nf := range block {
		if seen[nf] || spentIn(nf, height) {
			return false
		}
		seen[nf] = true
	}
	for h := range model {
		if h >= height {
			delete(model, h)
		}
	}
	if len(block) > 0 {
		model[height] = block
	}
	return true
}

func compareWalk(s *Store, model map[int64][][nodeLen]byte) string {
	var heights []int64
	for h := range model {
		heights = append(heights, h)
	}
	sort.Slice(heights, func(i, j int) bool { return heights[i] < heights[j] })
	var want [][nodeLen]byte
	for _, h := range heights {
		want = append(want, model[h]...)
	}
	var got [][nodeLen]byte
	if err := s.Walk(func(nf [nodeLen]byte, _ int64) error { got = append(got, nf); return nil }); err != nil {
		return "Walk: " + err.Error()
	}
	if len(got) != len(want) {
		return "Walk returns a different number of records than the model holds"
	}
	for i := range got {
		if got[i] != want[i] {
			return "Walk order differs from the model"
		}
	}
	return ""
}

// FuzzNullifierFold: Fold is a pure function of its inputs, order matters, and chaining it over a
// list is the accumulator the app hash commits to (so two different lists never share a prefix
// accumulator by accident of the encoding).
func FuzzNullifierFold(f *testing.F) {
	f.Add([]byte{1, 2, 3}, []byte{4, 5, 6})
	f.Add([]byte{}, []byte{})
	f.Fuzz(func(t *testing.T, a, b []byte) {
		var x, y [nodeLen]byte
		copy(x[:], a)
		copy(y[:], b)
		first, second := Fold(x, y), Fold(x, y)
		if first != second {
			t.Fatal("Fold is not deterministic")
		}
		if x != y && first == Fold(y, x) {
			t.Fatalf("Fold(%x, %x) is symmetric, so the order of nullifiers would not be committed", x, y)
		}
	})
}
