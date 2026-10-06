package storagefile

import "fmt"

// Rewrap turns one slot's ciphertext into another slot's ciphertext.
// It uses only the repair seed, so the file key stays wrapped and the
// plaintext is not recovered. The same deal's other slot bytes match.
func Rewrap(repairSeed, dealNonce []byte, from, to uint32, blob []byte) ([]byte, error) {
	if len(repairSeed) < keyLen {
		return nil, ErrNotForKey
	}
	if len(dealNonce) != DealNonceLen {
		return nil, fmt.Errorf("deal nonce must be %d bytes", DealNonceLen)
	}
	inner, err := applyOuter(repairSeed, dealNonce, from, blob)
	if err != nil {
		return nil, err
	}
	return applyOuter(repairSeed, dealNonce, to, inner)
}
