//go:build cgo && orchardffi

package orchard

import (
	"bytes"
	"errors"
	"sync"
	"testing"

	"github.com/DeBrosOfficial/network/chain/x/shielded/verify"
)

func TestVerify_vectorsAccept(t *testing.T) {
	if !Linked {
		t.Fatal("cgo build must report Linked")
	}
	for _, name := range vectorNames {
		bundle, _, chainID := loadVector(t, name)
		if err := New(chainID).Verify(bundle); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestVerify_wrongChainIDRejects(t *testing.T) {
	bundle, _, chainID := loadVector(t, "ironwood-1-action")
	err := New(chainID + "-other").Verify(bundle)
	if !errors.Is(err, verify.ErrSignatureRejected) {
		t.Fatalf("got %v, want ErrSignatureRejected", err)
	}
}

func TestVerify_flippedByteRejects(t *testing.T) {
	for _, name := range vectorNames {
		bundle, _, chainID := loadVector(t, name)
		n := int(bundle[0])
		proofLen := 2720 + 2272*n
		proofAt := 1 + n*actionLen + bundleHeaderLen + 3
		sigsAt := proofAt + proofLen
		cases := map[string]struct {
			at   int
			want error
		}{
			"proof":             {proofAt + 200, verify.ErrProofRejected},
			"epk of an action":  {1 + 32*4 + 3, verify.ErrSignatureRejected},
			"ciphertext":        {1 + 32*5 + 100, verify.ErrSignatureRejected},
			"anchor":            {1 + n*actionLen + 9 + 4, verify.ErrSignatureRejected},
			"spend-auth sig":    {sigsAt + 5, verify.ErrSignatureRejected},
			"binding signature": {len(bundle) - 10, verify.ErrSignatureRejected},
			"value balance":     {1 + n*actionLen + 1 + 2, verify.ErrSignatureRejected},
		}
		for label, c := range cases {
			mut := bytes.Clone(bundle)
			mut[c.at] ^= 1
			if err := New(chainID).Verify(mut); !errors.Is(err, c.want) {
				t.Errorf("%s/%s: got %v, want %v", name, label, err, c.want)
			}
		}
	}
}

func TestVerify_sighashByteFlipRejects(t *testing.T) {
	// The sighash is derived inside Verify, so a flipped sighash is a different chain ID.
	bundle, _, chainID := loadVector(t, "ironwood-2-action")
	if err := New(chainID[:len(chainID)-1] + "2").Verify(bundle); !errors.Is(err, verify.ErrSignatureRejected) {
		t.Fatalf("got %v", err)
	}
}

func TestVerify_wrongProofLengthRefusedBeforeVerify(t *testing.T) {
	bundle, _, chainID := loadVector(t, "ironwood-1-action")
	lenAt := 1 + actionLen + bundleHeaderLen
	if bundle[lenAt] != 0xfd {
		t.Fatalf("expected a 3-byte proof length, got %#x", bundle[lenAt])
	}
	short := bytes.Clone(bundle)
	short[lenAt+1]-- // claims one byte fewer than canonical
	if err := New(chainID).Verify(short); !errors.Is(err, verify.ErrProofLength) {
		t.Fatalf("short: got %v", err)
	}
	padded := bytes.Clone(bundle)
	padded[lenAt+1]++
	if err := New(chainID).Verify(padded); !errors.Is(err, verify.ErrProofLength) {
		t.Fatalf("padded: got %v", err)
	}
}

func TestVerify_malformedInputRefused(t *testing.T) {
	bundle, _, chainID := loadVector(t, "ironwood-1-action")
	v := New(chainID)
	for name, in := range map[string][]byte{
		"nil":       nil,
		"empty":     {},
		"one byte":  {1},
		"truncated": bundle[:len(bundle)-1],
		"trailing":  append(bytes.Clone(bundle), 0),
		"oversize":  make([]byte, MaxBundleBytes+1),
	} {
		err := v.Verify(in)
		if err == nil || !errors.Is(err, verify.ErrTampered) {
			t.Errorf("%s: got %v, want a rejection", name, err)
		}
	}
}

// One linked verifier is not enough: the chain needs two independent ones.
func TestCheck_oneRealVerifierStillFailsClosed(t *testing.T) {
	bundle, _, chainID := loadVector(t, "ironwood-1-action")
	if err := verify.Check(bundle, New(chainID)); !errors.Is(err, verify.ErrVerifierNotLinked) {
		t.Fatalf("got %v", err)
	}
}

func TestVerify_concurrent(t *testing.T) {
	bundle1, _, chainID := loadVector(t, "ironwood-1-action")
	bundle2, _, _ := loadVector(t, "ironwood-2-action")
	v := New(chainID)
	bad := bytes.Clone(bundle1)
	bad[len(bad)-10] ^= 1

	const workers = 16
	var wg sync.WaitGroup
	errs := make(chan error, workers*3)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- v.Verify(bundle1)
			errs <- v.Verify(bundle2)
			if err := v.Verify(bad); !errors.Is(err, verify.ErrSignatureRejected) {
				errs <- errors.New("tampered bundle was not rejected: " + errString(err))
				return
			}
			errs <- nil
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
}

func errString(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}

func benchmarkVerify(b *testing.B, name string) {
	bundle, _, chainID := loadVector(b, name)
	v := New(chainID)
	if err := v.Verify(bundle); err != nil { // builds the verifying key once, outside the timing
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := v.Verify(bundle); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkVerify_1action(b *testing.B) { benchmarkVerify(b, "ironwood-1-action") }
func BenchmarkVerify_2action(b *testing.B) { benchmarkVerify(b, "ironwood-2-action") }
