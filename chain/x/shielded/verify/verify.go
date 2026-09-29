// Package verify is the hook the Orchard/Ironwood verifiers fill.
// The first verifier is orchard (upstream orchard 0.15.x over cgo). Two independent
// verifiers are required before a bundle is accepted (plans/open-network/track-c-chain.md
// C12), so with one linked, Check still fails closed. A missing verifier fails closed.
// This package does not implement a circuit.
package verify

import (
	"errors"
	"fmt"
)

// MinVerifiers is the number of independent verifiers that must accept a bundle.
const MinVerifiers = 2

// ErrVerifierNotLinked means no proof checker is registered.
var ErrVerifierNotLinked = errors.New("shielded proof verifier is not linked")

// ErrTampered is returned by tests and by a verifier that rejects bytes.
var ErrTampered = errors.New("shielded bundle rejected")

// The verifier rejection reasons. Each one wraps ErrTampered.
var (
	// ErrMalformed means the bytes are not a canonical Ironwood bundle.
	ErrMalformed = fmt.Errorf("%w: malformed encoding", ErrTampered)
	// ErrProofLength means the proof is not the canonical size for the action count.
	ErrProofLength = fmt.Errorf("%w: proof length is not canonical for the action count", ErrTampered)
	// ErrProofRejected means the Halo 2 proof does not verify.
	ErrProofRejected = fmt.Errorf("%w: proof rejected", ErrTampered)
	// ErrSignatureRejected means a spend-authorization or the binding signature does not
	// verify over the bundle's sighash.
	ErrSignatureRejected = fmt.Errorf("%w: signature rejected", ErrTampered)
)

// ErrVerifierFault means the verifier failed internally (a caught panic or a bad call). The
// bundle is refused; this is not a statement about the bundle.
var ErrVerifierFault = errors.New("shielded verifier fault")

// Verifier checks one bundle. Nodes only verify. They do not build proofs.
type Verifier interface {
	Verify(bundle []byte) error
}

// Check runs every linked verifier. All of them must accept. Fewer than MinVerifiers,
// a missing verifier, or a single rejection, fails the bundle.
func Check(bundle []byte, verifiers ...Verifier) error {
	if len(verifiers) < MinVerifiers {
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
