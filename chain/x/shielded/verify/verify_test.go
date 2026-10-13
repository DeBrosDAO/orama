package verify

import (
	"bytes"
	"errors"
	"testing"
)

type scripted struct {
	name   string
	reject []byte
}

func (s scripted) ID() string { return s.name }

func (s scripted) Verify(bundle, _ []byte) error {
	if bytes.Equal(bundle, s.reject) {
		return ErrTampered
	}
	return nil
}

func TestNilVerifierFailsClosed(t *testing.T) {
	if err := Check([]byte{1}, nil); err != ErrVerifierNotLinked {
		t.Fatalf("got %v", err)
	}
	if err := Check([]byte{1}, nil, nil); err != ErrVerifierNotLinked {
		t.Fatalf("nil verifier: %v", err)
	}
}

func TestOneVerifierIsNotEnough(t *testing.T) {
	if err := Check([]byte{1}, nil, scripted{name: "a"}); err != ErrVerifierNotLinked {
		t.Fatalf("one verifier must fail closed: %v", err)
	}
}

func TestSameVerifierTwiceIsNotTwoVerifiers(t *testing.T) {
	a := scripted{name: "orchard"}
	if err := Check([]byte{1}, nil, a, a); !errors.Is(err, ErrDuplicateVerifier) || !errors.Is(err, ErrVerifierNotLinked) {
		t.Fatalf("the same verifier twice must fail closed: %v", err)
	}
	if err := Check([]byte{1}, nil, scripted{name: "orchard"}, scripted{name: "orchard"}); !errors.Is(err, ErrDuplicateVerifier) {
		t.Fatalf("two verifiers with one identity: %v", err)
	}
	if err := Check([]byte{1}, nil, scripted{name: "a"}, scripted{}); !errors.Is(err, ErrDuplicateVerifier) {
		t.Fatalf("a verifier with no identity: %v", err)
	}
}

func TestNothingRunsUnlessTheSetIsValid(t *testing.T) {
	ran := 0
	probe := countingVerifier{name: "a", ran: &ran}
	if err := Check([]byte{1}, nil, probe, probe); err == nil {
		t.Fatal("duplicate verifiers were accepted")
	}
	if ran != 0 {
		t.Fatalf("a verifier ran %d times before the set was validated", ran)
	}
}

type countingVerifier struct {
	name string
	ran  *int
}

func (c countingVerifier) ID() string                  { return c.name }
func (c countingVerifier) Verify([]byte, []byte) error { *c.ran++; return nil }

func TestBothVerifiersMustAccept(t *testing.T) {
	ok := scripted{name: "a"}
	bad := scripted{name: "b", reject: []byte{9}}
	if err := Check([]byte{1}, nil, ok, scripted{name: "c"}); err != nil {
		t.Fatal(err)
	}
	if err := Check([]byte{9}, nil, ok, bad); err != ErrTampered {
		t.Fatalf("tamper: %v", err)
	}
}
