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

	"github.com/DeBrosOfficial/network/chain/x/shielded/bundle"
)

// VerifierID is this verifier's identity for verify.Check's distinctness rule.
const VerifierID = "orchard"

const (
	// sighashDomain separates this hash from every other SHA-256 use.
	sighashDomain = "orama-shielded-ironwood-sighash-v1"
	// sighashBoundDomain is the domain of a sighash that commits to a binding as well.
	sighashBoundDomain = "orama-shielded-ironwood-sighash-bound-v1"

	// SighashLen is the sighash size in bytes.
	SighashLen = sha256.Size

	// MaxBundleBytes bounds the input before any parsing or proving work. It is far above
	// any bundle the action limits will allow.
	MaxBundleBytes = bundle.MaxBytes

	actionLen       = bundle.ActionLen
	bundleHeaderLen = bundle.HeaderLen
	// maxChainIDLen is what the u16 length prefix can carry.
	maxChainIDLen = 1<<16 - 1
)

// Sighash is the message every signature in the bundle signs. Without a binding:
//
//	SHA-256( "orama-shielded-ironwood-sighash-v1" || u16be(len(chainID)) || chainID || effectingData )
//
// With one, under its own domain so the two can never collide:
//
//	SHA-256( "orama-shielded-ironwood-sighash-bound-v1" || u16be(len(chainID)) || chainID
//	         || u32be(len(binding)) || binding || effectingData )
//
// The binding of an unshield is its signer and target: without it anyone who saw the bundle in the
// mempool could submit it under their own name and take what it unshields.
//
// The chain ID stops a bundle signed for one Orama network from replaying on another. The
// proof is bound by the circuit's public inputs, and the effecting data by this hash.
func Sighash(chainID string, binding, b []byte) ([SighashLen]byte, error) {
	var out [SighashLen]byte
	if len(chainID) > maxChainIDLen {
		return out, fmt.Errorf("chain id is %d bytes, the limit is %d", len(chainID), maxChainIDLen)
	}
	data, err := bundle.EffectingData(b)
	if err != nil {
		return out, err
	}
	h := sha256.New()
	if len(binding) == 0 {
		h.Write([]byte(sighashDomain))
	} else {
		h.Write([]byte(sighashBoundDomain))
	}
	var l [2]byte
	binary.BigEndian.PutUint16(l[:], uint16(len(chainID)))
	h.Write(l[:])
	h.Write([]byte(chainID))
	if len(binding) != 0 {
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], uint32(len(binding)))
		h.Write(n[:])
		h.Write(binding)
	}
	h.Write(data)
	h.Sum(out[:0])
	return out, nil
}
