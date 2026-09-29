// Package storagefile builds the bytes a private storage deal uploads.
//
// The inner layer is XChaCha20-Poly1305 under a random file key. That key is
// wrapped by HKDF-SHA256 of the owner seed with info "orama-storage-v1".
// Each slot then XORs a ChaCha20 keystream (zero nonce) whose 32-byte key is
// HKDF-SHA256 of the repair seed with info deal_nonce || slot, slot as 4
// big-endian bytes. The piece root
// is the commitment of that slot's ciphertext.
package storagefile

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/chacha20"
	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"

	"github.com/DeBrosOfficial/network/pkg/pieceroot"
)

const (
	magic = "ORSF"
	ver   = 1

	// DealNonceLen matches x/storage's deal_nonce width.
	DealNonceLen = 32
	keyLen       = 32
	nonceLen     = chacha20poly1305.NonceSizeX
)

// ErrNotForKey means the seed or the repair seed cannot open this blob.
var ErrNotForKey = errors.New("storage file cannot be opened with this key")

// Slot is one replica's ciphertext and the piece root a deal records.
type Slot struct {
	Index           uint32
	Bytes           []byte
	Root            []byte
	RealLeafCount   uint64
	PaddedLeafCount uint64
}

// Prepare seals plaintext and returns one distinct replica per slot.
func Prepare(seed, repairSeed, dealNonce []byte, replicas int, plaintext []byte) ([]Slot, error) {
	if len(seed) < keyLen {
		return nil, errors.New("storage seed must be at least 32 bytes")
	}
	if len(repairSeed) < keyLen {
		return nil, errors.New("repair seed must be at least 32 bytes")
	}
	if len(dealNonce) != DealNonceLen {
		return nil, fmt.Errorf("deal nonce must be %d bytes", DealNonceLen)
	}
	if replicas < 1 || replicas > 32 {
		return nil, errors.New("replicas must be from 1 to 32")
	}
	inner, err := sealInner(seed, plaintext)
	if err != nil {
		return nil, err
	}
	out := make([]Slot, replicas)
	for i := 0; i < replicas; i++ {
		blob, err := applyOuter(repairSeed, dealNonce, uint32(i), inner)
		if err != nil {
			return nil, err
		}
		c, err := pieceroot.Commit(blob)
		if err != nil {
			return nil, err
		}
		out[i] = Slot{
			Index: uint32(i), Bytes: blob, Root: c.Root,
			RealLeafCount: c.RealLeafCount, PaddedLeafCount: c.PaddedLeafCount,
		}
	}
	return out, nil
}

// Open strips slot's outer layer and decrypts the inner file.
func Open(seed, repairSeed, dealNonce []byte, slot uint32, blob []byte) ([]byte, error) {
	if len(seed) < keyLen || len(repairSeed) < keyLen {
		return nil, ErrNotForKey
	}
	if len(dealNonce) != DealNonceLen {
		return nil, fmt.Errorf("deal nonce must be %d bytes", DealNonceLen)
	}
	inner, err := applyOuter(repairSeed, dealNonce, slot, blob)
	if err != nil {
		return nil, err
	}
	plain, err := openInner(seed, inner)
	if err != nil {
		return nil, ErrNotForKey
	}
	return plain, nil
}

func sealInner(seed, plaintext []byte) ([]byte, error) {
	wrapKey, err := derive(seed, []byte("orama-storage-v1"), keyLen)
	if err != nil {
		return nil, err
	}
	fileKey := make([]byte, keyLen)
	if _, err := rand.Read(fileKey); err != nil {
		return nil, err
	}
	wrapNonce := make([]byte, nonceLen)
	contentNonce := make([]byte, nonceLen)
	if _, err := rand.Read(wrapNonce); err != nil {
		return nil, err
	}
	if _, err := rand.Read(contentNonce); err != nil {
		return nil, err
	}
	wrapped, err := xseal(wrapKey, wrapNonce, fileKey)
	if err != nil {
		return nil, err
	}
	body, err := xseal(fileKey, contentNonce, plaintext)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, 5+nonceLen+len(wrapped)+nonceLen+len(body))
	out = append(out, magic...)
	out = append(out, ver)
	out = append(out, wrapNonce...)
	out = append(out, wrapped...)
	out = append(out, contentNonce...)
	out = append(out, body...)
	return out, nil
}

func openInner(seed, blob []byte) ([]byte, error) {
	wrapKey, err := derive(seed, []byte("orama-storage-v1"), keyLen)
	if err != nil {
		return nil, err
	}
	need := 5 + nonceLen + keyLen + 16 + nonceLen + 16
	if len(blob) < need || string(blob[:4]) != magic || blob[4] != ver {
		return nil, errors.New("not an Orama storage file")
	}
	off := 5
	wrapNonce := blob[off : off+nonceLen]
	off += nonceLen
	wrapped := blob[off : off+keyLen+16]
	off += keyLen + 16
	contentNonce := blob[off : off+nonceLen]
	off += nonceLen
	fileKey, err := xopen(wrapKey, wrapNonce, wrapped)
	if err != nil {
		return nil, ErrNotForKey
	}
	plain, err := xopen(fileKey, contentNonce, blob[off:])
	if err != nil {
		return nil, ErrNotForKey
	}
	return plain, nil
}

// applyOuter XORs body with the slot keystream: ChaCha20 under the slot key
// HKDF-SHA256(repair seed, info deal_nonce || slot), with an all-zero nonce.
// Each (deal_nonce, slot) has its own key, so the zero nonce never repeats
// under one key. The same call removes the layer.
func applyOuter(repairSeed, dealNonce []byte, slot uint32, body []byte) ([]byte, error) {
	var j [4]byte
	binary.BigEndian.PutUint32(j[:], slot)
	info := append(append([]byte{}, dealNonce...), j[:]...)
	key, err := derive(repairSeed, info, chacha20.KeySize)
	if err != nil {
		return nil, err
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

func derive(secret, info []byte, n int) ([]byte, error) {
	if n < 0 {
		return nil, errors.New("negative derive length")
	}
	r := hkdf.New(sha256.New, secret, nil, info)
	out := make([]byte, n)
	if _, err := io.ReadFull(r, out); err != nil {
		return nil, err
	}
	return out, nil
}

func xseal(key, nonce, plaintext []byte) ([]byte, error) {
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, err
	}
	if len(nonce) != aead.NonceSize() {
		return nil, errors.New("bad nonce length")
	}
	return aead.Seal(nil, nonce, plaintext, nil), nil
}

func xopen(key, nonce, ciphertext []byte) ([]byte, error) {
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, err
	}
	plain, err := aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, err
	}
	return plain, nil
}
