package globalbind

import (
	"crypto/ed25519"
	"crypto/sha256"
	"fmt"

	secp256k1 "github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
)

// Verify checks that the binding's signature is over SignBytes for this chain
// and operator. secp256k1 uses the Cosmos SHA-256 digest and rejects a high S.
func Verify(b Binding, chainID, operator string) error {
	msg := SignBytes(chainID, operator, b.Service, b.Pubkey)
	switch b.KeyType {
	case KeyTypeSecp256k1:
		if len(b.Signature) != 64 || len(b.Pubkey) != 33 {
			return fmt.Errorf("secp256k1 binding has the wrong size")
		}
		pub, err := secp256k1.ParsePubKey(b.Pubkey)
		if err != nil {
			return fmt.Errorf("secp256k1 pubkey: %w", err)
		}
		var r, s secp256k1.ModNScalar
		if r.SetByteSlice(b.Signature[:32]) || s.SetByteSlice(b.Signature[32:]) {
			return fmt.Errorf("secp256k1 signature is not canonical")
		}
		if s.IsOverHalfOrder() {
			return fmt.Errorf("secp256k1 signature is not low-S")
		}
		sum := sha256.Sum256(msg)
		if !ecdsa.NewSignature(&r, &s).Verify(sum[:], pub) {
			return fmt.Errorf("secp256k1 binding signature does not verify")
		}
	case KeyTypeEd25519, KeyTypeEd25519Expanded:
		if len(b.Pubkey) != ed25519.PublicKeySize || len(b.Signature) != ed25519.SignatureSize {
			return fmt.Errorf("ed25519 binding has the wrong size")
		}
		if !ed25519.Verify(b.Pubkey, msg, b.Signature) {
			return fmt.Errorf("ed25519 binding signature does not verify")
		}
	default:
		return fmt.Errorf("unknown key type %s", b.KeyType)
	}
	return nil
}
