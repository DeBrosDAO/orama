// Package shamir implements Shamir's Secret Sharing over GF(2^8).
//
// Uses the AES irreducible polynomial x^8 + x^4 + x^3 + x + 1 (0x11B)
// with generator 3. Precomputed log/exp tables for O(1) field arithmetic.
//
// Cross-platform compatible with the Zig (orama-vault) and TypeScript
// (network-ts-sdk) implementations using identical field parameters.
package shamir

import "errors"

// ErrDivisionByZero is returned when dividing by zero in GF(2^8).
var ErrDivisionByZero = errors.New("shamir: division by zero in GF(2^8)")

// Irreducible polynomial: x^8 + x^4 + x^3 + x + 1.
const irreducible = 0x11B

// expTable[i] = generator^i mod polynomial, for i in 0..511.
// Extended to 512 entries so Mul can use (logA + logB) without modular reduction.
var expTable [512]byte

// logTable[a] = i where generator^i = a, for a in 1..255.
// logTable[0] is unused (log of zero is undefined).
var logTable [256]byte

func init() {
	x := uint16(1)
	for i := 0; i < 512; i++ {
		if i < 256 {
			expTable[i] = byte(x)
			logTable[byte(x)] = byte(i)
		} else {
			expTable[i] = expTable[i-255]
		}

		if i < 255 {
			// Multiply by generator (3): x*3 = x*2 XOR x
			x2 := x << 1
			x3 := x2 ^ x
			if x3&0x100 != 0 {
				x3 ^= irreducible
			}
			x = x3
		}
	}
}

// Add returns a XOR b (addition in GF(2^8)).
func Add(a, b byte) byte {
	return a ^ b
}

// Mul returns a * b in GF(2^8) via log/exp tables.
func Mul(a, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}
	logSum := uint16(logTable[a]) + uint16(logTable[b])
	return expTable[logSum]
}

// Inv returns the multiplicative inverse of a in GF(2^8).
// Returns ErrDivisionByZero if a == 0.
func Inv(a byte) (byte, error) {
	if a == 0 {
		return 0, ErrDivisionByZero
	}
	return expTable[255-uint16(logTable[a])], nil
}

// Div returns a / b in GF(2^8).
// Returns ErrDivisionByZero if b == 0.
func Div(a, b byte) (byte, error) {
	if b == 0 {
		return 0, ErrDivisionByZero
	}
	if a == 0 {
		return 0, nil
	}
	logDiff := uint16(logTable[a]) + 255 - uint16(logTable[b])
	return expTable[logDiff], nil
}
