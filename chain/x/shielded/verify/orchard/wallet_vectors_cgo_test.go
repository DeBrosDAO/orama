//go:build cgo && orchardffi

package orchard

import (
	"bytes"
	"errors"
	"testing"

	"github.com/DeBrosOfficial/network/chain/x/shielded/verify"
)

// Every bundle the F7 wallet builder produced, including a transfer and an unshield that spend
// real notes, must pass the same verifier the chain runs.
func TestVerify_walletBuilderBundlesAccept(t *testing.T) {
	s := loadWalletScenario(t)
	v := New(s.ChainID)
	for _, step := range s.Steps {
		if err := v.Verify(mustHex(t, step.Bundle)); err != nil {
			t.Errorf("%s (%s): %v", step.Name, step.Kind, err)
		}
	}
}

func TestVerify_walletBuilderBundlesRejectTampering(t *testing.T) {
	s := loadWalletScenario(t)
	v := New(s.ChainID)
	for _, step := range s.Steps {
		bundle := mustHex(t, step.Bundle)
		n := int(bundle[0])
		proofAt := 1 + n*actionLen + bundleHeaderLen + 3
		sigsAt := proofAt + 2720 + 2272*n
		cases := map[string]struct {
			at   int
			want error
		}{
			"proof":             {proofAt + 200, verify.ErrProofRejected},
			"nullifier":         {1 + 32, verify.ErrSignatureRejected},
			"ciphertext":        {1 + 32*5 + 100, verify.ErrSignatureRejected},
			"anchor":            {1 + n*actionLen + 9 + 4, verify.ErrSignatureRejected},
			"value balance":     {1 + n*actionLen + 1 + 2, verify.ErrSignatureRejected},
			"spend-auth sig":    {sigsAt + 5, verify.ErrSignatureRejected},
			"binding signature": {len(bundle) - 10, verify.ErrSignatureRejected},
		}
		for label, c := range cases {
			mut := bytes.Clone(bundle)
			mut[c.at] ^= 1
			err := v.Verify(mut)
			if label == "nullifier" {
				// nf is a public input of the proof and is also under the sighash.
				if err == nil || !errors.Is(err, verify.ErrTampered) {
					t.Errorf("%s/%s: got %v, want a rejection", step.Name, label, err)
				}
				continue
			}
			if !errors.Is(err, c.want) {
				t.Errorf("%s/%s: got %v, want %v", step.Name, label, err, c.want)
			}
		}
		if err := New(s.ChainID + "-other").Verify(bundle); !errors.Is(err, verify.ErrSignatureRejected) {
			t.Errorf("%s: wrong chain id: got %v", step.Name, err)
		}
	}
}
