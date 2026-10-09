package storageclient

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/storagefile"
)

// lose makes slot i a replacement: its provider holds nothing and the chain
// has assigned it again without an acceptance.
func (w *world) lose(i int) {
	delete(w.live, uint64(i))
	w.providers[i].pieces = map[string][]byte{}
}

func (w *world) holdAll(t *testing.T) {
	t.Helper()
	w.live = map[uint64]bool{}
	for i, p := range w.providers {
		p.refusals = 0
		p.pieces[hex.EncodeToString(w.slots[i].Root)] = w.slots[i].Bytes
		w.live[uint64(i)] = true
	}
}

func TestRepair_rebuildsALostSlotFromAnotherProvider(t *testing.T) {
	plain := bytes.Repeat([]byte("orama "), 700)
	w := newWorld(t, plain)
	w.holdAll(t)
	w.lose(1)
	c := w.client(t)

	done, err := c.Repair(context.Background(), 5, testRepair)
	if err != nil {
		t.Fatal(err)
	}
	if len(done) != 1 || done[0].Slot != 1 {
		t.Fatalf("repaired %+v, want only slot 1", done)
	}
	got := w.providers[1].pieces[hex.EncodeToString(w.slots[1].Root)]
	if !bytes.Equal(got, w.slots[1].Bytes) {
		t.Fatal("the new provider does not hold slot 1's replica")
	}
	w.live[1] = true
	w.providers[0].down = true
	w.providers[2].down = true
	opened, err := c.Get(context.Background(), 5, testSeed, testRepair)
	if err != nil || !bytes.Equal(opened, plain) {
		t.Fatalf("the repaired replica does not open: %v", err)
	}
}

func TestRepair_wrongSeedUploadsNothing(t *testing.T) {
	w := newWorld(t, []byte("payload"))
	w.holdAll(t)
	w.lose(2)
	c := w.client(t)
	_, err := c.Repair(context.Background(), 5, bytes.Repeat([]byte{1}, 32))
	if !errors.Is(err, ErrRootMismatch) {
		t.Fatalf("got %v, want ErrRootMismatch", err)
	}
	if len(w.providers[2].pieces) != 0 {
		t.Fatal("a replica built with the wrong seed was uploaded")
	}
	_, err = c.Repair(context.Background(), 5, []byte("short"))
	if !errors.Is(err, storagefile.ErrNotForKey) {
		t.Fatalf("a short seed: %v", err)
	}
}

func TestRepair_nothingToDoAndNothingToCopy(t *testing.T) {
	w := newWorld(t, []byte("payload"))
	w.holdAll(t)
	c := w.client(t)
	done, err := c.Repair(context.Background(), 5, testRepair)
	if err != nil || len(done) != 0 {
		t.Fatalf("a whole deal needs no repair: %v %+v", err, done)
	}

	w.live = map[uint64]bool{}
	_, err = c.Repair(context.Background(), 5, testRepair)
	if !errors.Is(err, ErrNoSource) {
		t.Fatalf("a deal with no accepted replica: got %v, want ErrNoSource", err)
	}
}

func TestRepair_skipsADeadSourceAndUsesAnother(t *testing.T) {
	w := newWorld(t, []byte("payload"))
	w.holdAll(t)
	w.lose(2)
	w.providers[0].down = true
	c := w.client(t)
	done, err := c.Repair(context.Background(), 5, testRepair)
	if err != nil || len(done) != 1 || done[0].From != 1 {
		t.Fatalf("got %v %+v, want slot 2 rebuilt from slot 1", err, done)
	}
}
