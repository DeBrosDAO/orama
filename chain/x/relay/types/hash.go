package types

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
)

// inputsRootVersion is the first byte of the canonical entry encoding.
// Bumping it changes every inputs_root.
const inputsRootVersion byte = 1

// InputsRoot is SHA-256 over the canonical encoding of entries, in order.
// Reporters must set MsgReportEpoch.inputs_root to this value for the
// reassembled chunks. A mutated entry does not match. This is the on-chain
// commitment; binding the same root to archived vote files (E2) is outside
// this module.
func InputsRoot(entries []RelayObservation) ([]byte, error) {
	var buf []byte
	buf = append(buf, inputsRootVersion)
	for i, entry := range entries {
		encoded, err := canonicalEntry(entry)
		if err != nil {
			return nil, fmt.Errorf("entry %d: %w", i, err)
		}
		buf = append(buf, encoded...)
	}
	sum := sha256.Sum256(buf)
	return sum[:], nil
}

func canonicalEntry(entry RelayObservation) ([]byte, error) {
	if entry.ConsensusWeight.IsNil() {
		return nil, fmt.Errorf("consensus weight is unset")
	}
	if entry.UptimeFraction.IsNil() {
		return nil, fmt.Errorf("uptime is unset")
	}
	var buf []byte
	var err error
	buf, err = appendBytes(buf, entry.RsaFingerprint)
	if err != nil {
		return nil, err
	}
	buf, err = appendBytes(buf, entry.Ed25519Id)
	if err != nil {
		return nil, err
	}
	buf, err = appendBytes(buf, []byte(entry.ConsensusWeight.String()))
	if err != nil {
		return nil, err
	}
	var flags [4]byte
	binary.BigEndian.PutUint32(flags[:], entry.Flags)
	buf = append(buf, flags[:]...)
	return appendBytes(buf, []byte(entry.UptimeFraction.String()))
}

func appendBytes(buf, field []byte) ([]byte, error) {
	if len(field) > math.MaxUint16 {
		return nil, fmt.Errorf("canonical field is longer than uint16")
	}
	var n [2]byte
	binary.BigEndian.PutUint16(n[:], uint16(len(field)))
	buf = append(buf, n[:]...)
	return append(buf, field...), nil
}
