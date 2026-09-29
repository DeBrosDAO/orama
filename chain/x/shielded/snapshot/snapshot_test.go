package snapshot

import (
	"errors"
	"io"
	"testing"

	dbm "github.com/cosmos/cosmos-db"

	"github.com/DeBrosOfficial/network/chain/x/shielded/bundle"
	"github.com/DeBrosOfficial/network/chain/x/shielded/nullifier"
	"github.com/DeBrosOfficial/network/chain/x/shielded/types"
)

func nf(i int) [bundle.NodeLen]byte {
	var n [bundle.NodeLen]byte
	n[0], n[1], n[2] = byte(i), byte(i>>8), byte(i>>16)
	n[31] = 1
	return n
}

// fill commits `blocks` blocks of `per` nullifiers each, returning what the state would commit to.
func fill(t *testing.T, s *nullifier.Store, blocks, per int) (acc [bundle.NodeLen]byte, count uint64) {
	t.Helper()
	i := 0
	for h := 1; h <= blocks; h++ {
		var block [][bundle.NodeLen]byte
		for j := 0; j < per; j++ {
			n := nf(i)
			i++
			block = append(block, n)
			acc = nullifier.Fold(acc, n)
			count++
		}
		if err := s.Commit(int64(h), block); err != nil {
			t.Fatal(err)
		}
	}
	return acc, count
}

func payloads(t *testing.T, e *Extension, height uint64) [][]byte {
	t.Helper()
	var out [][]byte
	err := e.SnapshotExtension(height, func(p []byte) error { out = append(out, append([]byte(nil), p...)); return nil })
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func reader(ps [][]byte) func() ([]byte, error) {
	i := 0
	return func() ([]byte, error) {
		if i == len(ps) {
			return nil, io.EOF
		}
		i++
		return ps[i-1], nil
	}
}

func TestSnapshotRestore_roundTripSpansPayloads(t *testing.T) {
	src := nullifier.NewStore(dbm.NewMemDB())
	acc, count := fill(t, src, 3, recordsPerPayload) // 3 payloads' worth
	committed := func(uint64) ([bundle.NodeLen]byte, uint64, error) { return acc, count, nil }
	ps := payloads(t, New(src, committed), 3)
	if len(ps) != 3 {
		t.Fatalf("got %d payloads", len(ps))
	}

	dst := nullifier.NewStore(dbm.NewMemDB())
	if err := New(dst, committed).RestoreExtension(3, Format, reader(ps)); err != nil {
		t.Fatal(err)
	}
	for _, i := range []int{0, recordsPerPayload, 3*recordsPerPayload - 1} {
		if spent, _ := dst.Spent(nf(i), 100); !spent {
			t.Errorf("nullifier %d did not survive the restore", i)
		}
	}
	var got [bundle.NodeLen]byte
	_ = dst.Walk(func(n [bundle.NodeLen]byte, _ int64) error { got = nullifier.Fold(got, n); return nil })
	if got != acc {
		t.Fatal("the restored log folds differently")
	}
}

// The database can hold the records of a block that ran but did not commit; the snapshot must not
// carry them.
func TestSnapshot_excludesUncommittedRecords(t *testing.T) {
	src := nullifier.NewStore(dbm.NewMemDB())
	acc, count := fill(t, src, 2, 3)
	if err := src.Commit(3, [][bundle.NodeLen]byte{nf(1000)}); err != nil {
		t.Fatal(err)
	}
	committed := func(uint64) ([bundle.NodeLen]byte, uint64, error) { return acc, count, nil }
	dst := nullifier.NewStore(dbm.NewMemDB())
	if err := New(dst, committed).RestoreExtension(2, Format, reader(payloads(t, New(src, committed), 2))); err != nil {
		t.Fatal(err)
	}
	if spent, _ := dst.Spent(nf(1000), 100); spent {
		t.Fatal("an uncommitted record travelled")
	}
}

func TestRestore_refusesWhatDoesNotFoldToTheCommittedAccumulator(t *testing.T) {
	src := nullifier.NewStore(dbm.NewMemDB())
	acc, count := fill(t, src, 2, 3)
	ps := payloads(t, New(src, func(uint64) ([bundle.NodeLen]byte, uint64, error) { return acc, count, nil }), 2)

	tampered := append([]byte(nil), ps[0]...)
	tampered[20] ^= 1 // a nullifier byte
	wrong := New(nullifier.NewStore(dbm.NewMemDB()), func(uint64) ([bundle.NodeLen]byte, uint64, error) { return acc, count, nil })
	if err := wrong.RestoreExtension(2, Format, reader([][]byte{tampered})); !errors.Is(err, types.ErrNullifierStore) {
		t.Fatalf("tampered payload: %v", err)
	}
	short := New(nullifier.NewStore(dbm.NewMemDB()), func(uint64) ([bundle.NodeLen]byte, uint64, error) { return acc, count + 1, nil })
	if err := short.RestoreExtension(2, Format, reader(ps)); !errors.Is(err, types.ErrNullifierStore) {
		t.Fatalf("missing nullifier: %v", err)
	}
}

func TestRestore_refusesMalformedInput(t *testing.T) {
	committed := func(uint64) ([bundle.NodeLen]byte, uint64, error) { return [bundle.NodeLen]byte{}, 0, nil }
	e := New(nullifier.NewStore(dbm.NewMemDB()), committed)
	if err := e.RestoreExtension(1, 99, reader(nil)); err == nil {
		t.Error("an unknown format was accepted")
	}
	if err := e.RestoreExtension(1, Format, reader([][]byte{{1, 2, 3}})); !errors.Is(err, types.ErrNullifierStore) {
		t.Errorf("partial record: %v", err)
	}
	above := append(make([]byte, 7), 9) // height 9 in a snapshot at height 1
	above = append(above, make([]byte, bundle.NodeLen)...)
	if err := e.RestoreExtension(1, Format, reader([][]byte{above})); !errors.Is(err, types.ErrNullifierStore) {
		t.Errorf("record above the snapshot height: %v", err)
	}
	busy := nullifier.NewStore(dbm.NewMemDB())
	_, _ = fill(t, busy, 1, 1)
	if err := New(busy, committed).RestoreExtension(1, Format, reader(nil)); !errors.Is(err, types.ErrNullifierStore) {
		t.Errorf("restore into a non-empty store: %v", err)
	}
}

func TestSnapshot_storeShorterThanCommittedFails(t *testing.T) {
	src := nullifier.NewStore(dbm.NewMemDB())
	_, count := fill(t, src, 1, 2)
	e := New(src, func(uint64) ([bundle.NodeLen]byte, uint64, error) { return [bundle.NodeLen]byte{}, count + 5, nil })
	if err := e.SnapshotExtension(1, func([]byte) error { return nil }); !errors.Is(err, types.ErrNullifierStore) {
		t.Fatalf("got %v", err)
	}
}
