package verify

import (
	"bytes"
	"testing"
)

type scripted struct {
	reject []byte
}

func (s scripted) Verify(bundle []byte) error {
	if bytes.Equal(bundle, s.reject) {
		return ErrTampered
	}
	return nil
}

func TestNilVerifierFailsClosed(t *testing.T) {
	if err := Check([]byte{1}); err != ErrVerifierNotLinked {
		t.Fatalf("got %v", err)
	}
	if err := Check([]byte{1}, nil); err != ErrVerifierNotLinked {
		t.Fatalf("nil verifier: %v", err)
	}
}

func TestBothVerifiersMustAccept(t *testing.T) {
	ok := scripted{}
	bad := scripted{reject: []byte{9}}
	if err := Check([]byte{1}, ok, ok); err != nil {
		t.Fatal(err)
	}
	if err := Check([]byte{9}, ok, bad); err != ErrTampered {
		t.Fatalf("tamper: %v", err)
	}
}
