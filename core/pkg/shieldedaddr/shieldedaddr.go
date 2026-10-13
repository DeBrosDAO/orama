// Package shieldedaddr is the text form of an Orama shielded address: the 43 raw bytes of an
// Orchard address (an 11-byte diversifier and the 32-byte transmission key) in Bech32m (BIP-350)
// with the human-readable part "oramas". It is 82 characters, written in lowercase; a decoder also
// accepts all capitals and refuses a mix of cases.
//
// This is the Go twin of the Rust codec in chain/x/shielded/wallet/src/address.rs and of the
// TypeScript one in sdk/src/chain/shielded-address.ts. The three check a string in the same order
// and report the same codes, and all read the one vector file,
// chain/x/shielded/wallet/testdata/addresses/addresses.json.
//
// The codec is structural: it accepts any 43 bytes. Whether they are an address Orchard can pay (a
// curve point) is for the wallet that builds the bundle.
package shieldedaddr

import (
	"errors"
	"fmt"
	"strings"
)

const (
	// HRP is the human-readable part of a shielded address.
	HRP = "oramas"
	// RawLen is the bytes in a raw Orchard address.
	RawLen = 43
	// dataLen is the characters after the separator: 69 for the address (344 bits and one zero
	// padding bit) and 6 of checksum.
	dataLen     = 75
	checksumLen = 6
	// EncodedLen is the characters in an encoded address.
	EncodedLen = len(HRP) + 1 + dataLen

	charset = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"
	// bech32mConst is the constant the Bech32m checksum xors in (BIP-350); Bech32 xors in 1.
	bech32mConst = 0x2bc830a3
)

// Code names why a string is not a shielded address. The values are the names the vector file uses.
type Code string

const (
	CodeMixedCase    Code = "mixed_case"
	CodeNoSeparator  Code = "no_separator"
	CodeWrongPrefix  Code = "wrong_prefix"
	CodeBadCharacter Code = "bad_character"
	CodeWrongLength  Code = "wrong_length"
	CodeBadChecksum  Code = "bad_checksum"
	CodeBadPadding   Code = "bad_padding"
)

var messages = map[Code]string{
	CodeMixedCase:    "mixes upper and lower case",
	CodeNoSeparator:  "has no '1' separator",
	CodeWrongPrefix:  "does not start with oramas1",
	CodeBadCharacter: "has a character outside the Bech32 alphabet",
	CodeWrongLength:  "is not 82 characters long",
	CodeBadChecksum:  "has a wrong Bech32m checksum",
	CodeBadPadding:   "has non-zero padding bits",
}

// Error is the reason a string is not a shielded address.
type Error struct{ Code Code }

func (e *Error) Error() string { return "not a shielded address: it " + messages[e.Code] }

// CodeOf is the Code of an error from Decode, or "" for any other error.
func CodeOf(err error) Code {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// Encode is the shielded address text of a raw Orchard address, in lowercase.
func Encode(raw [RawLen]byte) string {
	words := convert(raw[:], 8, 5, true)
	data := append(words, checksum(HRP, words)...)
	var b strings.Builder
	b.WriteString(HRP)
	b.WriteByte('1')
	for _, w := range data {
		b.WriteByte(charset[w])
	}
	return b.String()
}

// Decode is the raw 43 bytes of a shielded address, or the first reason s is not one: mixed case,
// no separator, a prefix other than oramas, a character outside the alphabet, a data part other
// than 75 characters, the Bech32m checksum (an address with the original Bech32 checksum fails
// here) and a non-zero padding bit, in that order.
func Decode(s string) ([RawLen]byte, error) {
	var out [RawLen]byte
	s, mixed := lowerASCII(s)
	if mixed {
		return out, &Error{CodeMixedCase}
	}
	sep := strings.LastIndexByte(s, '1')
	if sep < 0 {
		return out, &Error{CodeNoSeparator}
	}
	if s[:sep] != HRP {
		return out, &Error{CodeWrongPrefix}
	}
	data := make([]byte, 0, dataLen)
	for i := sep + 1; i < len(s); i++ {
		v := strings.IndexByte(charset, s[i])
		if v < 0 {
			return out, &Error{CodeBadCharacter}
		}
		data = append(data, byte(v))
	}
	if len(data) != dataLen {
		return out, &Error{CodeWrongLength}
	}
	if polymod(append(hrpExpand(HRP), data...)) != bech32mConst {
		return out, &Error{CodeBadChecksum}
	}
	raw, ok := convertStrict(data[:dataLen-checksumLen])
	if !ok {
		return out, &Error{CodeBadPadding}
	}
	if len(raw) != RawLen {
		return out, fmt.Errorf("a data part of %d characters decoded to %d bytes: %w", dataLen, len(raw), &Error{CodeWrongLength})
	}
	copy(out[:], raw)
	return out, nil
}

// IsAddress reports whether s is a shielded address.
func IsAddress(s string) bool {
	_, err := Decode(s)
	return err == nil
}

// lowerASCII lowercases the ASCII letters of s and reports whether it had both cases of them.
func lowerASCII(s string) (string, bool) {
	var upper, lower bool
	b := []byte(s)
	for i, c := range b {
		switch {
		case c >= 'A' && c <= 'Z':
			upper, b[i] = true, c+'a'-'A'
		case c >= 'a' && c <= 'z':
			lower = true
		}
	}
	return string(b), upper && lower
}

func polymod(values []byte) uint32 {
	gen := [5]uint32{0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3}
	chk := uint32(1)
	for _, v := range values {
		top := chk >> 25
		chk = (chk&0x1ffffff)<<5 ^ uint32(v)
		for i := 0; i < 5; i++ {
			if (top>>i)&1 == 1 {
				chk ^= gen[i]
			}
		}
	}
	return chk
}

func hrpExpand(hrp string) []byte {
	out := make([]byte, 0, 2*len(hrp)+1)
	for i := 0; i < len(hrp); i++ {
		out = append(out, hrp[i]>>5)
	}
	out = append(out, 0)
	for i := 0; i < len(hrp); i++ {
		out = append(out, hrp[i]&31)
	}
	return out
}

func checksum(hrp string, data []byte) []byte {
	values := append(append(hrpExpand(hrp), data...), make([]byte, checksumLen)...)
	mod := polymod(values) ^ bech32mConst
	out := make([]byte, checksumLen)
	for i := range out {
		out[i] = byte((mod >> (5 * (checksumLen - 1 - i))) & 31)
	}
	return out
}

// convert regroups bits from frombits-wide values into tobits-wide ones, zero-padding the last.
func convert(data []byte, frombits, tobits uint, pad bool) []byte {
	var acc, bits uint
	maxv := uint(1)<<tobits - 1
	var out []byte
	for _, v := range data {
		acc = acc<<frombits | uint(v)
		bits += frombits
		for bits >= tobits {
			bits -= tobits
			out = append(out, byte((acc>>bits)&maxv))
		}
	}
	if pad && bits > 0 {
		out = append(out, byte((acc<<(tobits-bits))&maxv))
	}
	return out
}

// convertStrict regroups 5-bit values into bytes; the bits left after the last byte must be zero.
func convertStrict(words []byte) ([]byte, bool) {
	var acc, bits uint
	var out []byte
	for _, w := range words {
		acc = acc<<5 | uint(w)
		bits += 5
		for bits >= 8 {
			bits -= 8
			out = append(out, byte(acc>>bits))
		}
	}
	return out, bits == 0 || acc&(1<<bits-1) == 0
}
