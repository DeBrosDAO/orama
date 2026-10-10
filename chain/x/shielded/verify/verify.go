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

// ErrDuplicateVerifier means two of the supplied verifiers share an identity, or one has none, so
// they are not independent. It wraps ErrVerifierNotLinked: the required number of distinct
// verifiers is not linked.
var ErrDuplicateVerifier = fmt.Errorf("%w: verifiers must be distinct and named", ErrVerifierNotLinked)

// ErrEmptyChainID means a verifier was asked to bind to no chain. A bundle signed for the empty
// chain id would verify on every chain that forgot to set one.
var ErrEmptyChainID = errors.New("shielded verifier needs a chain id")

// ErrVerifierFault means the verifier failed internally (a caught panic or a bad call). The
// bundle is refused; this is not a statement about the bundle.
var ErrVerifierFault = errors.New("shielded verifier fault")

// Verifier checks one bundle. Nodes only verify. They do not build proofs.
//
// binding is what the signatures must additionally commit to: nil for a bundle whose transparent
// side needs no protection, and for an unshield the signer and target, so a copied bundle cannot be
// redirected to another account (see orchard.Sighash).
type Verifier interface {
	// ID names the implementation (for example "orchard"). Two verifiers count as independent
	// only when their IDs differ, so the same verifier passed twice cannot satisfy MinVerifiers.
	ID() string
	Verify(bundle, binding []byte) error
}

// Check runs every linked verifier. All of them must accept. Fewer than MinVerifiers, a missing
// verifier, fewer than MinVerifiers distinct verifier IDs, or a single rejection, fails the bundle.
// No verifier runs unless the whole set is valid.
func Check(bundle, binding []byte, verifiers ...Verifier) error {
	if len(verifiers) < MinVerifiers {
		return ErrVerifierNotLinked
	}
	ids := make(map[string]struct{}, len(verifiers))
	for _, v := range verifiers {
		if v == nil {
			return ErrVerifierNotLinked
		}
		id := v.ID()
		if _, dup := ids[id]; dup || id == "" {
			return ErrDuplicateVerifier
		}
		ids[id] = struct{}{}
	}
	for _, v := range verifiers {
		if err := v.Verify(bundle, binding); err != nil {
			return err
		}
	}
	return nil
}
