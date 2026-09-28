package storagefile

import (
	"bytes"
	"testing"
)

func TestRewrapMatchesTheOtherSlot(t *testing.T) {
	seed := bytes.Repeat([]byte{1}, 32)
	repair := bytes.Repeat([]byte{2}, 32)
	nonce := bytes.Repeat([]byte{3}, DealNonceLen)
	slots, err := Prepare(seed, repair, nonce, 3, []byte("repair me"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := Rewrap(repair, nonce, 0, 2, slots[0].Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, slots[2].Bytes) {
		t.Fatal("rewrap did not reproduce the surviving slot")
	}
	plain, err := Open(seed, repair, nonce, 2, got)
	if err != nil || string(plain) != "repair me" {
		t.Fatalf("open %q %v", plain, err)
	}
	if _, err := Open(seed, repair, nonce, 0, got); err == nil {
		t.Fatal("rewrapped bytes opened as the old slot")
	}
	if _, err := Rewrap(bytes.Repeat([]byte{9}, 16), nonce, 0, 1, slots[0].Bytes); err == nil {
		t.Fatal("short repair seed was accepted")
	}
}
