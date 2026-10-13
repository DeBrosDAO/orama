package secrets

import "testing"

func TestKeyset_legacyWriteThenVersionedRead(t *testing.T) {
	root := Root{CurrentID: "1", CurrentIKM: "ikm-one"}
	ks, err := root.Keyset("purpose-a")
	if err != nil {
		t.Fatal(err)
	}
	ct, err := ks.Encrypt("secret")
	if err != nil {
		t.Fatal(err)
	}
	if IsEncrypted(ct) == false {
		t.Fatal(ct)
	}
	got, err := ks.Decrypt(ct)
	if err != nil {
		t.Fatal(err)
	}
	if got != "secret" {
		t.Fatalf("got %q", got)
	}
}

func TestKeyset_rotateCannotOpenNewWithOld(t *testing.T) {
	old := Root{CurrentID: "1", CurrentIKM: "ikm-old", WriteVersioned: true}
	oldKS, err := old.Keyset("p")
	if err != nil {
		t.Fatal(err)
	}
	next := Root{
		CurrentID:      "2",
		CurrentIKM:     "ikm-new",
		PreviousID:     "1",
		PreviousIKM:    "ikm-old",
		WriteVersioned: true,
	}
	newKS, err := next.Keyset("p")
	if err != nil {
		t.Fatal(err)
	}

	oldCT, err := oldKS.Encrypt("old-value")
	if err != nil {
		t.Fatal(err)
	}
	newCT, err := newKS.Encrypt("new-value")
	if err != nil {
		t.Fatal(err)
	}

	got, err := newKS.Decrypt(oldCT)
	if err != nil {
		t.Fatalf("new keyset should still open previous generation: %v", err)
	}
	if got != "old-value" {
		t.Fatalf("got %q", got)
	}

	if _, err := oldKS.Decrypt(newCT); err == nil {
		t.Fatal("old keyset opened a row sealed after rotate — snapshot did not age out")
	}
}

func TestHolder_swapIsVisible(t *testing.T) {
	h := NewHolder(Root{CurrentID: "1", CurrentIKM: "ikm-old"})
	ks, err := h.Keyset("p")
	if err != nil {
		t.Fatal(err)
	}
	ct, err := ks.Encrypt("v")
	if err != nil {
		t.Fatal(err)
	}
	h.Swap(Root{CurrentID: "2", CurrentIKM: "ikm-new", PreviousID: "1", PreviousIKM: "ikm-old", WriteVersioned: true})
	ks2, err := h.Keyset("p")
	if err != nil {
		t.Fatal(err)
	}
	got, err := ks2.Decrypt(ct)
	if err != nil {
		t.Fatal(err)
	}
	if got != "v" {
		t.Fatalf("got %q", got)
	}
}
