package shamir

import (
	"crypto/rand"
	"errors"
	"fmt"
)

var (
	ErrThresholdTooSmall   = errors.New("shamir: threshold K must be at least 2")
	ErrShareCountTooSmall  = errors.New("shamir: share count N must be >= threshold K")
	ErrTooManyShares       = errors.New("shamir: maximum 255 shares (GF(2^8) limit)")
	ErrEmptySecret         = errors.New("shamir: secret must not be empty")
	ErrNotEnoughShares     = errors.New("shamir: need at least 2 shares to reconstruct")
	ErrMismatchedShareLen  = errors.New("shamir: all shares must have the same data length")
	ErrZeroShareIndex      = errors.New("shamir: share index must not be 0")
	ErrDuplicateShareIndex = errors.New("shamir: duplicate share indices")
)

// Share represents a single Shamir share.
type Share struct {
	X byte   // Evaluation point (1..255, never 0)
	Y []byte // Share data (same length as original secret)
}

// Split divides secret into n shares with threshold k.
// Any k shares can reconstruct the secret; k-1 reveal nothing.
func Split(secret []byte, n, k int) ([]Share, error) {
	if k < 2 {
		return nil, ErrThresholdTooSmall
	}
	if n < k {
		return nil, ErrShareCountTooSmall
	}
	if n > 255 {
		return nil, ErrTooManyShares
	}
	if len(secret) == 0 {
		return nil, ErrEmptySecret
	}

	shares := make([]Share, n)
	for i := range shares {
		shares[i] = Share{
			X: byte(i + 1),
			Y: make([]byte, len(secret)),
		}
	}

	// Temporary buffer for polynomial coefficients.
	coeffs := make([]byte, k)
	defer func() {
		for i := range coeffs {
			coeffs[i] = 0
		}
	}()

	for byteIdx := 0; byteIdx < len(secret); byteIdx++ {
		coeffs[0] = secret[byteIdx]
		// Fill degrees 1..k-1 with random bytes.
		if _, err := rand.Read(coeffs[1:]); err != nil {
			return nil, fmt.Errorf("shamir: random generation failed: %w", err)
		}
		for i := range shares {
			shares[i].Y[byteIdx] = evaluatePolynomial(coeffs, shares[i].X)
		}
	}

	return shares, nil
}

// Combine reconstructs the secret from k or more shares via Lagrange interpolation.
func Combine(shares []Share) ([]byte, error) {
	if len(shares) < 2 {
		return nil, ErrNotEnoughShares
	}

	secretLen := len(shares[0].Y)
	seen := make(map[byte]bool, len(shares))
	for _, s := range shares {
		if s.X == 0 {
			return nil, ErrZeroShareIndex
		}
		if len(s.Y) != secretLen {
			return nil, ErrMismatchedShareLen
		}
		if seen[s.X] {
			return nil, ErrDuplicateShareIndex
		}
		seen[s.X] = true
	}

	result := make([]byte, secretLen)
	for byteIdx := 0; byteIdx < secretLen; byteIdx++ {
		var value byte
		for i, si := range shares {
			// Lagrange basis polynomial L_i evaluated at 0:
			// L_i(0) = product over j!=i of (0 - x_j)/(x_i - x_j)
			//         = product over j!=i of x_j / (x_i XOR x_j)
			var basis byte = 1
			for j, sj := range shares {
				if i == j {
					continue
				}
				num := sj.X
				den := Add(si.X, sj.X) // x_i - x_j = x_i XOR x_j in GF(2^8)
				d, err := Div(num, den)
				if err != nil {
					return nil, err
				}
				basis = Mul(basis, d)
			}
			value = Add(value, Mul(si.Y[byteIdx], basis))
		}
		result[byteIdx] = value
	}

	return result, nil
}

// AdaptiveThreshold returns the read threshold K = max(2, floor(n/3)).
// K is the minimum number of shares required to reconstruct the secret; any
// K-1 shares reveal zero information. The floor is 2 (not 3) because Split
// itself requires only K>=2, and pairing K=2 with WriteQuorum's W>K invariant
// gives small clusters (e.g. N=3) read fault tolerance while keeping a
// successful write recoverable.
func AdaptiveThreshold(n int) int {
	t := n / 3
	if t < 2 {
		return 2
	}
	return t
}

// WriteQuorum returns the write quorum W = min(n, max(K+1, ceil(2n/3))).
//
// The invariant W > K is the durability guarantee: any push reported successful
// has persisted strictly more shares than a read requires, so a "successful"
// write is always recoverable and survives the loss of at least one guardian.
// The previous formula (ceil(2n/3), with K floored at 3) produced W=2 < K=3 at
// n=3, so a push could report success while storing too few shares to ever read
// back — silent, permanent data loss.
func WriteQuorum(n int) int {
	if n <= 0 {
		return 0
	}
	k := AdaptiveThreshold(n)
	w := (2*n + 2) / 3 // ceil(2n/3)
	if w < k+1 {
		w = k + 1
	}
	if w > n {
		w = n
	}
	return w
}

// evaluatePolynomial evaluates p(x) = coeffs[0] + coeffs[1]*x + ... using Horner's method.
func evaluatePolynomial(coeffs []byte, x byte) byte {
	var result byte
	for i := len(coeffs) - 1; i >= 0; i-- {
		result = Add(Mul(result, x), coeffs[i])
	}
	return result
}
