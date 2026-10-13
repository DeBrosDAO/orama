// Package storagekey is the per-slot outer layer of a private storage deal.
// It is the same construction as core/pkg/storagefile, which seals files on
// the client; both are locked to testdata/outer_vectors.json. The repair
// delegate uses it to turn one slot's bytes into another slot's bytes with
// the repair seed alone. The inner layer, and so the plaintext, stays sealed.
package storagekey

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/chacha20"
	"golang.org/x/crypto/hkdf"
)

const (
	// MinSeedLen is the shortest repair seed accepted.
	MinSeedLen = 32
	// DealNonceLen matches x/storage's deal_nonce width.
	DealNonceLen = 32
)

// ErrSeed is a repair seed too short to be one.
var ErrSeed = errors.New("repair seed must be at least 32 bytes")

// Apply XORs body with slot's keystream: ChaCha20 with an all-zero nonce under
// the key HKDF-SHA256(repairSeed, salt nil, info dealNonce || slot big-endian).
// Applying it twice returns body.
func Apply(repairSeed, dealNonce []byte, slot uint32, body []byte) ([]byte, error) {
	if len(repairSeed) < MinSeedLen {
		return nil, ErrSeed
	}
	if len(dealNonce) != DealNonceLen {
		return nil, fmt.Errorf("deal nonce must be %d bytes", DealNonceLen)
	}
	var j [4]byte
	binary.BigEndian.PutUint32(j[:], slot)
	info := append(append([]byte{}, dealNonce...), j[:]...)
	key := make([]byte, chacha20.KeySize)
	if _, err := io.ReadFull(hkdf.New(sha256.New, repairSeed, nil, info), key); err != nil {
		return nil, fmt.Errorf("derive slot %d key: %w", slot, err)
	}
	var zero [chacha20.NonceSize]byte
	stream, err := chacha20.NewUnauthenticatedCipher(key, zero[:])
	if err != nil {
		return nil, err
	}
	out := make([]byte, len(body))
	stream.XORKeyStream(out, body)
	return out, nil
}

// Rewrap turns slot from's bytes into slot to's bytes.
func Rewrap(repairSeed, dealNonce []byte, from, to uint32, blob []byte) ([]byte, error) {
	inner, err := Apply(repairSeed, dealNonce, from, blob)
	if err != nil {
		return nil, err
	}
	return Apply(repairSeed, dealNonce, to, inner)
}
