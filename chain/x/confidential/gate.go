// Package confidential is the gate for a confidential node.
// A report is refused. This package does not invent a TEE measurement
// or treat a blob as proof that the operator cannot read the machine.
package confidential

import "errors"

// ErrNoAttestation is returned for every report, including an empty one.
var ErrNoAttestation = errors.New("confidential node refused: no TEE attestation is accepted")

// Accept refuses report. A non-empty report is not treated as valid.
func Accept(report []byte) error {
	_ = report
	return ErrNoAttestation
}
