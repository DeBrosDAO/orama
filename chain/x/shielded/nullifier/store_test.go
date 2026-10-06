package nullifier

import (
	"errors"
	"testing"

	dbm "github.com/cosmos/cosmos-db"
)

func nf(b byte) [nodeLen]byte {
	var n [nodeLen]byte
	for i := range n {
		n[i] = b
	}
	return n
}

func TestStore_visibleOnlyBelowTheReadersHeight(t *testing.T) {
	s := NewStore(dbm.NewMemDB())
	if err := s.Commit(5, [][nodeLen]byte{nf(1)}); err != nil {
		t.Fatal(err)
	}
	for asOf, want := range map[int64]bool{5: false, 6: true, 100: true, 1: false} {
		got, err := s.Spent(nf(1), asOf)
		if err != nil || got != want {
			t.Errorf("asOf %d: got %v, %v, want %v", asOf, got, err, want)
		}
	}
	if got, _ := s.Spent(nf(2), 100); got {
		t.Error("an unknown nullifier reads as spent")
	}
}

// A block that was executed but not committed leaves its records behind. Replaying the height must
// not see them, and must replace them.
func TestStore_replayOfAHeightReplacesItsRecords(t *testing.T) {
	s := NewStore(dbm.NewMemDB())
	must(t, s.Commit(1, [][nodeLen]byte{nf(1)}))
	must(t, s.Commit(2, [][nodeLen]byte{nf(2), nf(3)}))

	if spent, _ := s.Spent(nf(2), 2); spent {
		t.Fatal("height 2 must not see its own records when it is replayed")
	}
	must(t, s.Commit(2, [][nodeLen]byte{nf(4)}))

	for n, want := range map[byte]bool{1: true, 2: false, 3: false, 4: true} {
		if got, _ := s.Spent(nf(n), 3); got != want {
			t.Errorf("nullifier %d: spent=%v, want %v", n, got, want)
		}
	}
	var walked []byte
	must(t, s.Walk(func(n [nodeLen]byte, _ int64) error { walked = append(walked, n[0]); return nil }))
	if string(walked) != string([]byte{1, 4}) {
		t.Fatalf("walk order %v, want [1 4]", walked)
	}
}

// After a rollback the store still holds later heights. The reader at the rolled-back height must
// not see them, and committing that height again clears them.
func TestStore_rollbackHidesAndThenDropsLaterHeights(t *testing.T) {
	s := NewStore(dbm.NewMemDB())
	must(t, s.Commit(1, [][nodeLen]byte{nf(1)}))
	must(t, s.Commit(2, [][nodeLen]byte{nf(2)}))
	must(t, s.Commit(3, [][nodeLen]byte{nf(3)}))
	if spent, _ := s.Spent(nf(3), 2); spent {
		t.Fatal("a record from a later height is visible")
	}
	must(t, s.Commit(2, nil))
	if empty, _ := s.Spent(nf(3), 100); empty {
		t.Fatal("height 3 survived a commit at height 2")
	}
}

func TestStore_walkIsInsertionOrder(t *testing.T) {
	s := NewStore(dbm.NewMemDB())
	must(t, s.Commit(1, [][nodeLen]byte{nf(9), nf(1), nf(5)}))
	must(t, s.Commit(2, [][nodeLen]byte{nf(2)}))
	var got []byte
	must(t, s.Walk(func(n [nodeLen]byte, _ int64) error { got = append(got, n[0]); return nil }))
	if string(got) != string([]byte{9, 1, 5, 2}) {
		t.Fatalf("got %v", got)
	}
}

func TestStore_refusesAnAlreadySpentNullifier(t *testing.T) {
	s := NewStore(dbm.NewMemDB())
	must(t, s.Commit(1, [][nodeLen]byte{nf(1)}))
	if err := s.Commit(2, [][nodeLen]byte{nf(1)}); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("got %v", err)
	}
	if err := s.Commit(3, [][nodeLen]byte{nf(7), nf(7)}); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("duplicate in one block: got %v", err)
	}
}

// The node's default database backend is pebbledb (docs/CHAIN.md); goleveldb is what the SDK
// defaults to. The store must persist across a restart on both.
func TestStore_emptyAndPersistence(t *testing.T) {
	for _, backend := range []dbm.BackendType{dbm.GoLevelDBBackend, dbm.PebbleDBBackend} {
		t.Run(string(backend), func(t *testing.T) {
			dir := t.TempDir()
			db, err := dbm.NewDB("n", backend, dir)
			must(t, err)
			s := NewStore(db)
			if empty, err := s.Empty(); err != nil || !empty {
				t.Fatalf("new store: %v %v", empty, err)
			}
			must(t, s.Commit(4, [][nodeLen]byte{nf(8)}))
			must(t, s.Import([][nodeLen]byte{nf(3)}))
			must(t, s.Close())

			db, err = dbm.NewDB("n", backend, dir)
			must(t, err)
			s = NewStore(db)
			defer s.Close()
			for _, n := range []byte{8, 3} {
				if got, _ := s.Spent(nf(n), 5); !got {
					t.Fatalf("nullifier %d did not survive a restart", n)
				}
			}
			if empty, _ := s.Empty(); empty {
				t.Fatal("store reads empty after a write")
			}
		})
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestStore_importIsVisibleToAnyHeightAndKeepsItsOrder(t *testing.T) {
	s := NewStore(dbm.NewMemDB())
	must(t, s.Import([][nodeLen]byte{nf(9), nf(1)}))
	must(t, s.Import([][nodeLen]byte{nf(5)})) // the next chunk continues the order
	for _, n := range []byte{9, 1, 5} {
		if got, _ := s.Spent(nf(n), 1); !got {
			t.Errorf("imported nullifier %d is not visible to a chain starting at height 1", n)
		}
	}
	var got []byte
	must(t, s.Walk(func(n [nodeLen]byte, h int64) error {
		if h != 0 {
			t.Errorf("imported record at height %d", h)
		}
		got = append(got, n[0])
		return nil
	}))
	if string(got) != string([]byte{9, 1, 5}) {
		t.Fatalf("order %v", got)
	}
	must(t, s.Commit(1, [][nodeLen]byte{nf(7)}))
	if got, _ := s.Spent(nf(9), 2); !got {
		t.Fatal("a block's commit removed imported records")
	}
}

func TestStore_importRefusesADuplicate(t *testing.T) {
	s := NewStore(dbm.NewMemDB())
	if err := s.Import([][nodeLen]byte{nf(1), nf(1)}); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("got %v", err)
	}
}
