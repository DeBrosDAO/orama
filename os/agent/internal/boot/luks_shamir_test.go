package boot

import (
	"bytes"
	"testing"
)

func TestShamirCombineUsesStoredX(t *testing.T) {
	secret := []byte("orama-luks-key-material-32b!!")
	shares, err := shamirSplit(secret, 3, 2)
	if err != nil {
		t.Fatal(err)
	}
	// Skip the middle guardian: shares 1 and 3, which used to be
	// relabelled x=1,x=2 and reconstruct the wrong key.
	got, err := shamirCombine([][]byte{shares[0], shares[2]})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, secret) {
		t.Fatalf("reconstructed %q, want %q", got, secret)
	}
}

func TestShamirCombineRejectsDuplicateX(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcd")
	shares, err := shamirSplit(secret, 3, 2)
	if err != nil {
		t.Fatal(err)
	}
	dup := append([]byte(nil), shares[0]...)
	if _, err := shamirCombine([][]byte{shares[0], dup}); err == nil {
		t.Fatal("duplicate x-coordinate was accepted")
	}
}

func TestAdaptiveThreshold(t *testing.T) {
	if adaptiveThreshold(1) != 1 {
		t.Fatalf("n=1: got %d want 1", adaptiveThreshold(1))
	}
	if adaptiveThreshold(3) != 2 {
		t.Fatalf("n=3: got %d want 2", adaptiveThreshold(3))
	}
	if adaptiveThreshold(9) != 3 {
		t.Fatalf("n=9: got %d want 3", adaptiveThreshold(9))
	}
}
