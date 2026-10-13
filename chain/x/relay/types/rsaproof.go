package types

import (
	"bytes"
	"crypto"
	"crypto/rsa"
	"crypto/sha1" // #nosec G505 -- a Tor relay fingerprint is the SHA-1 of its RSA identity key, by definition
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"fmt"
)

// rsaProofDomain separates the RSA proof from every other message the same key might sign.
const rsaProofDomain = "orama/relay/rsa-proof/v2"

const (
	// MaxRSAPublicKeyLen bounds the DER of a relay's RSA identity key (a 4096-bit key is about 530 bytes).
	MaxRSAPublicKeyLen = 1024
	// MinRSABits and MaxRSABits bound the size of that key. Tor's identity keys are 1024 bits.
	MinRSABits = 1024
	MaxRSABits = 4096
)

// ErrRSAProofMismatch is returned when the RSA identity key and signature do not prove that the caller
// holds the key behind the fingerprint.
var ErrRSAProofMismatch = errors.New("rsa identity does not prove the fingerprint")

// RSAProofMessage is the byte string the relay's RSA identity key signs: the domain, the node id and the
// ed25519 key the node binds as its relay key and one exit byte (0 non-exit, 1 exit), each after a zero
// byte. It binds the RSA identity to that node, that ed25519 key and that exit declaration, so a proof
// made for one node, or for a non-exit relay, is useless for another node or for an exit.
func RSAProofMessage(nodeID string, ed25519Pub []byte, exit bool) []byte {
	msg := make([]byte, 0, len(rsaProofDomain)+1+len(nodeID)+1+len(ed25519Pub)+1+1)
	msg = append(msg, rsaProofDomain...)
	msg = append(msg, 0)
	msg = append(msg, nodeID...)
	msg = append(msg, 0)
	msg = append(msg, ed25519Pub...)
	msg = append(msg, 0)
	return append(msg, exitByte(exit))
}

// VerifyRSAProof checks that rsaPublicKey (PKCS#1 DER) is the key whose SHA-1 digest is fingerprint and
// that signature is its RSASSA-PKCS1-v1_5 SHA-256 signature over RSAProofMessage. A fingerprint is public
// (the consensus lists it), so without this anyone could register a stranger's fingerprint first.
func VerifyRSAProof(fingerprint, rsaPublicKey, ed25519Pub []byte, nodeID string, exit bool, signature []byte) error {
	if len(rsaPublicKey) == 0 || len(rsaPublicKey) > MaxRSAPublicKeyLen {
		return fmt.Errorf("%w: rsa public key must be 1 to %d bytes", ErrRSAProofMismatch, MaxRSAPublicKeyLen)
	}
	digest := sha1.Sum(rsaPublicKey) // #nosec G401
	if !bytes.Equal(digest[:], fingerprint) {
		return fmt.Errorf("%w: the rsa public key does not hash to the fingerprint", ErrRSAProofMismatch)
	}
	pub, err := x509.ParsePKCS1PublicKey(rsaPublicKey)
	if err != nil {
		return fmt.Errorf("%w: rsa public key: %v", ErrRSAProofMismatch, err)
	}
	if !bytes.Equal(x509.MarshalPKCS1PublicKey(pub), rsaPublicKey) {
		return fmt.Errorf("%w: rsa public key is not in canonical DER", ErrRSAProofMismatch)
	}
	if bits := pub.N.BitLen(); bits < MinRSABits || bits > MaxRSABits {
		return fmt.Errorf("%w: rsa key is %d bits, want %d to %d", ErrRSAProofMismatch, bits, MinRSABits, MaxRSABits)
	}
	sum := sha256.Sum256(RSAProofMessage(nodeID, ed25519Pub, exit))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], signature); err != nil {
		return fmt.Errorf("%w: the signature does not verify", ErrRSAProofMismatch)
	}
	return nil
}
