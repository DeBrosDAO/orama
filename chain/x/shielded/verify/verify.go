// Package verify is the hook a real Orchard/Ironwood verifier must fill.
// The production binary does not link one yet. A nil verifier fails closed.
// Two independent verifiers are required before this can accept a bundle
// (plans/open-network/track-c-chain.md C12). This package does not implement
// a circuit.
package verify

import "errors"

// ErrVerifierNotLinked means no proof checker is registered.
var ErrVerifierNotLinked = errors.New("shielded proof verifier is not linked")

// ErrTampered is returned by tests and by a verifier that rejects bytes.
var ErrTampered = errors.New("shielded bundle rejected")

// Verifier checks one bundle. Nodes only verify. They do not build proofs.
type Verifier interface {
	Verify(bundle []byte) error
}

// Check runs every linked verifier. All of them must accept. A missing
// verifier, or a single rejection, fails the bundle.
func Check(bundle []byte, verifiers ...Verifier) error {
	if len(verifiers) == 0 {
		return ErrVerifierNotLinked
	}
	for _, v := range verifiers {
		if v == nil {
			return ErrVerifierNotLinked
		}
		if err := v.Verify(bundle); err != nil {
			return err
		}
	}
	return nil
}
