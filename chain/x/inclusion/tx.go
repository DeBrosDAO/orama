package inclusion

import (
	"encoding/binary"
	"fmt"
)

const txVersion byte = 1

// Meta is the part of a raw transaction the inclusion rules read.
// The transaction's identity is its raw bytes, not this struct.
type Meta struct {
	Sender   []byte
	Sequence uint64
	Fee      uint64
	Payload  []byte
}

// SenderKey is the State.NextSequence map key for a sender.
func SenderKey(sender []byte) string {
	return string(sender)
}

// EncodeTx serialises a transaction the inclusion rules can decode.
// sender must be 1 to 255 bytes. payload may be empty.
func EncodeTx(sender []byte, sequence, fee uint64, payload []byte) ([]byte, error) {
	if len(sender) == 0 || len(sender) > 255 {
		return nil, fmt.Errorf("inclusion: sender length must be 1..255")
	}
	out := make([]byte, 2+len(sender)+16+len(payload))
	out[0] = txVersion
	out[1] = byte(len(sender))
	copy(out[2:], sender)
	binary.BigEndian.PutUint64(out[2+len(sender):], sequence)
	binary.BigEndian.PutUint64(out[2+len(sender)+8:], fee)
	copy(out[2+len(sender)+16:], payload)
	return out, nil
}

// DecodeTx parses a transaction produced by EncodeTx.
// It does not require a canonical payload; everything after the fee is payload.
func DecodeTx(raw []byte) (Meta, error) {
	if len(raw) < 2 {
		return Meta{}, fmt.Errorf("inclusion: transaction too short")
	}
	if raw[0] != txVersion {
		return Meta{}, fmt.Errorf("inclusion: unsupported transaction version %d", raw[0])
	}
	n := int(raw[1])
	if n == 0 {
		return Meta{}, fmt.Errorf("inclusion: empty sender")
	}
	if len(raw) < 2+n+16 {
		return Meta{}, fmt.Errorf("inclusion: transaction truncated")
	}
	sender := make([]byte, n)
	copy(sender, raw[2:2+n])
	seq := binary.BigEndian.Uint64(raw[2+n:])
	fee := binary.BigEndian.Uint64(raw[2+n+8:])
	payload := make([]byte, len(raw)-(2+n+16))
	copy(payload, raw[2+n+16:])
	return Meta{Sender: sender, Sequence: seq, Fee: fee, Payload: payload}, nil
}
