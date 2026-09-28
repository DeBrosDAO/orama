package nullifier

import "testing"

func TestInsertRejectsReplayAndAgreesAcrossCopies(t *testing.T) {
	a := New()
	b := New()
	var n1, n2 [32]byte
	n1[0] = 1
	n2[0] = 2
	if err := a.Insert(n1); err != nil {
		t.Fatal(err)
	}
	if err := a.Insert(n1); err != ErrSpent {
		t.Fatalf("replay: %v", err)
	}
	if err := a.Insert(n2); err != nil {
		t.Fatal(err)
	}
	if err := b.Insert(n1); err != nil || b.Insert(n2) != nil {
		t.Fatal("copy")
	}
	if a.Accumulator() != b.Accumulator() || a.Len() != 2 {
		t.Fatal("accumulators diverged")
	}
	// Order is part of the accumulator.
	c := New()
	if err := c.Insert(n2); err != nil || c.Insert(n1) != nil {
		t.Fatal(err)
	}
	if c.Accumulator() == a.Accumulator() {
		t.Fatal("order did not change the accumulator")
	}
}
