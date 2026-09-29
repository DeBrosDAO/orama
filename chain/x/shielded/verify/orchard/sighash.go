// Package orchard verifies Ironwood bundles with upstream orchard 0.15.x through cgo.
//
// The Rust side (chain/x/shielded/orchardffi) checks the proof, every spend-authorization
// signature and the binding signature over a sighash it is given. This package defines that
// sighash and computes it, so the Rust code never invents one.
package orchard

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"

	"github.com/DeBrosOfficial/network/chain/x/shielded/verify"
)

const (
	// sighashDomain separates this hash from every other SHA-256 use.
	sighashDomain = "orama-shielded-ironwood-sighash-v1"

	// SighashLen is the sighash size in bytes.
	SighashLen = sha256.Size

	// MaxBundleBytes bounds the input before any parsing or proving work. It is far above
	// any bundle the action limits will allow; the per-block limit is fixed by C12a.
	MaxBundleBytes = 1 << 20

	// actionLen is one serialized action: cv, nf, rk, cmx, epk (32 each), enc (580), out (80).
	actionLen = 5*32 + 580 + 80
	// bundleHeaderLen is flags (1) + value balance (8) + anchor (32).
	bundleHeaderLen = 1 + 8 + 32
	// maxChainIDLen is what the u16 length prefix can carry.
	maxChainIDLen = 1<<16 - 1
)

// readCompactSize reads a canonical Bitcoin-style CompactSize from the front of b and returns
// the value and the bytes it used.
func readCompactSize(b []byte) (value uint64, used int, ok bool) {
	if len(b) == 0 {
		return 0, 0, false
	}
	switch first := b[0]; {
	case first < 253:
		return uint64(first), 1, true
	case first == 253:
		if len(b) < 3 {
			return 0, 0, false
		}
		v := uint64(binary.LittleEndian.Uint16(b[1:]))
		return v, 3, v >= 253
	case first == 254:
		if len(b) < 5 {
			return 0, 0, false
		}
		v := uint64(binary.LittleEndian.Uint32(b[1:]))
		return v, 5, v >= 1<<16
	default:
		if len(b) < 9 {
			return 0, 0, false
		}
		v := binary.LittleEndian.Uint64(b[1:])
		return v, 9, v >= 1<<32
	}
}

// effectingData returns the bytes of a bundle that the signatures cover: the action count,
// every action (cv, nf, rk, cmx, epk, ciphertexts), the flags, the value balance and the anchor.
// In the canonical encoding this is the contiguous prefix before the proof, so the proof and
// the signatures are excluded and everything a wallet signs is included.
func effectingData(bundle []byte) ([]byte, error) {
	n, used, ok := readCompactSize(bundle)
	if !ok || n == 0 || n > MaxBundleBytes/actionLen {
		return nil, fmt.Errorf("%w: action count", verify.ErrMalformed)
	}
	end := used + int(n)*actionLen + bundleHeaderLen
	if end > len(bundle) {
		return nil, fmt.Errorf("%w: truncated before the proof", verify.ErrMalformed)
	}
	return bundle[:end], nil
}

// Sighash is the message every signature in the bundle signs:
//
//	SHA-256( "orama-shielded-ironwood-sighash-v1" || u16be(len(chainID)) || chainID || effectingData )
//
// The chain ID stops a bundle signed for one Orama network from replaying on another. The
// proof is bound by the circuit's public inputs, and the effecting data by this hash.
func Sighash(chainID string, bundle []byte) ([SighashLen]byte, error) {
	var out [SighashLen]byte
	if len(chainID) > maxChainIDLen {
		return out, fmt.Errorf("chain id is %d bytes, the limit is %d", len(chainID), maxChainIDLen)
	}
	data, err := effectingData(bundle)
	if err != nil {
		return out, err
	}
	h := sha256.New()
	h.Write([]byte(sighashDomain))
	var l [2]byte
	binary.BigEndian.PutUint16(l[:], uint16(len(chainID)))
	h.Write(l[:])
	h.Write([]byte(chainID))
	h.Write(data)
	h.Sum(out[:0])
	return out, nil
}
