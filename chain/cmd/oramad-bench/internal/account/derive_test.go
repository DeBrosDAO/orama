package account

import "testing"

func TestDerive_isDeterministicAndDistinct(t *testing.T) {
	a1, err := Derive("seed", 3)
	if err != nil {
		t.Fatal(err)
	}
	a2, err := Derive("seed", 3)
	if err != nil {
		t.Fatal(err)
	}
	if a1.Address != a2.Address {
		t.Fatal("the same seed and index gave two addresses")
	}
	other, _ := Derive("seed", 4)
	diff, _ := Derive("other", 3)
	if a1.Address == other.Address || a1.Address == diff.Address {
		t.Fatal("a different index or seed gave the same address")
	}
}

func TestDerive_refusesBadInput(t *testing.T) {
	if _, err := Derive("", 0); err == nil {
		t.Fatal("empty seed accepted")
	}
	if _, err := Derive("s", -1); err == nil {
		t.Fatal("negative index accepted")
	}
	if _, err := DeriveN("s", 0); err == nil {
		t.Fatal("zero accounts accepted")
	}
}

func TestDeriveN_returnsDistinctAccounts(t *testing.T) {
	accts, err := DeriveN("s", 20)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, a := range accts {
		if seen[a.Address] {
			t.Fatalf("duplicate address %s", a.Address)
		}
		seen[a.Address] = true
	}
}
