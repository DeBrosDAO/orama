// Package confidential is the software boundary for confidential nodes and
// the marketplace that would place tenants on them (plans/open-network/
// track-h-confidential-marketplace.md).
//
// Nothing here accepts an attestation. There is no linked SEV-SNP or TDX
// verifier, the vendor root registry is empty, and the marketplace is not
// live. A VerifiedReport can only be produced by a verifier in this package
// (its verified field is unexported), and no verifier in this package returns
// one. This package does not invent a TEE measurement, treat a blob as proof
// that the operator cannot read the machine, or accept a stand-in quote in any
// form. It is not imported by the app or any module, and a test keeps it that
// way until a real verifier ships.
package confidential

import "errors"

// ErrNoAttestation is returned for every report, including an empty one.
var ErrNoAttestation = errors.New("confidential node refused: no TEE attestation is accepted")

// Accept refuses report. A non-empty report is not treated as valid.
func Accept(report []byte) error {
	_ = report
	return ErrNoAttestation
}
