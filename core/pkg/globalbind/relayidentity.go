package globalbind

import (
	"bytes"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1" // #nosec G505 -- a Tor relay fingerprint is SHA-1 by definition
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/clusterreg"
)

// RelayIdentity is what registering a Tor relay on the chain needs from the relay's machine: the
// RSA fingerprint, the binding that makes the relay's ed25519 master identity a service key of the
// operator's node (service "relay"), the cross-certificate that ties the fingerprint to the node, and
// the RSA identity key with its proof: a fingerprint is public, so the chain asks the registrant to
// show that it holds the key behind it. It carries signatures and public keys only; the secrets stay in
// the relay's DataDirectory.
type RelayIdentity struct {
	// Fingerprint is the 20-byte SHA-1 digest of the relay's RSA identity key.
	Fingerprint []byte
	// Binding is the ed25519 binding of the relay's master identity for the operator on a chain.
	Binding Binding
	// CrossCert is the relay's ed25519 signature over clusterreg.RelayCrossCertMessage.
	CrossCert []byte
	// RSAPublicKey is the relay's RSA identity public key, PKCS#1 DER.
	RSAPublicKey []byte
	// RSASignature is the RSA identity's RSASSA-PKCS1-v1_5 SHA-256 signature over clusterreg.RelayRSAProofMessage.
	RSASignature []byte
}

// relayIdentityDoc is RelayIdentity as the node prints it: all binary values in hex.
type relayIdentityDoc struct {
	RSAFingerprint string `json:"rsa_fingerprint"`
	Service        string `json:"service"`
	KeyType        string `json:"key_type"`
	Pubkey         string `json:"pubkey"`
	Signature      string `json:"signature"`
	CrossCert      string `json:"cross_cert"`
	RSAPublicKey   string `json:"rsa_public_key"`
	RSASignature   string `json:"rsa_signature"`
}

// NewRelayIdentity signs, with tor's expanded ed25519 secret, the relay binding for operator on
// chainID and the cross-certificate of the RSA key's fingerprint for nodeID, and proves the RSA key
// with a signature of its own over the proof message of nodeID, the binding's key and exit. Both
// signatures cover exit, so they register or update the relay only with that declaration.
func NewRelayIdentity(expanded []byte, rsaKey *rsa.PrivateKey, chainID, operator, nodeID string, exit bool) (RelayIdentity, error) {
	if rsaKey == nil {
		return RelayIdentity{}, fmt.Errorf("the relay has no RSA identity key")
	}
	publicKey := x509.MarshalPKCS1PublicKey(&rsaKey.PublicKey)
	digest := sha1.Sum(publicKey) // #nosec G401 -- a Tor relay fingerprint is SHA-1 by definition
	fingerprint := digest[:]
	binding, err := SignExpandedEd25519(expanded, chainID, operator, clusterreg.RelayService)
	if err != nil {
		return RelayIdentity{}, err
	}
	cert, err := SignRelayCrossCert(expanded, nodeID, fingerprint, exit)
	if err != nil {
		return RelayIdentity{}, err
	}
	sum := sha256.Sum256(clusterreg.RelayRSAProofMessage(nodeID, binding.Pubkey, exit))
	proof, err := rsa.SignPKCS1v15(rand.Reader, rsaKey, crypto.SHA256, sum[:])
	if err != nil {
		return RelayIdentity{}, fmt.Errorf("sign the RSA proof: %w", err)
	}
	return RelayIdentity{Fingerprint: append([]byte(nil), fingerprint...), Binding: binding, CrossCert: cert, RSAPublicKey: publicKey, RSASignature: proof}, nil
}

// MarshalJSON prints the identity in the form ParseRelayIdentity reads.
func (r RelayIdentity) MarshalJSON() ([]byte, error) {
	return json.Marshal(relayIdentityDoc{
		RSAFingerprint: strings.ToUpper(hex.EncodeToString(r.Fingerprint)),
		Service:        r.Binding.Service,
		KeyType:        r.Binding.KeyType,
		Pubkey:         hex.EncodeToString(r.Binding.Pubkey),
		Signature:      hex.EncodeToString(r.Binding.Signature),
		CrossCert:      hex.EncodeToString(r.CrossCert),
		RSAPublicKey:   hex.EncodeToString(r.RSAPublicKey),
		RSASignature:   hex.EncodeToString(r.RSASignature),
	})
}

// ParseRelayIdentity reads what a relay's machine printed and checks it for the operator on chainID
// and the node nodeID, before anything is signed or sent: the binding must be the relay service's
// ed25519 key, its signature must verify, and the cross-certificate must verify under that same
// key for this node and fingerprint. A value the chain would refuse is refused here.
func ParseRelayIdentity(raw []byte, chainID, operator, nodeID string, exit bool) (RelayIdentity, error) {
	var doc relayIdentityDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return RelayIdentity{}, fmt.Errorf("the relay identity is not JSON: %w", err)
	}
	fingerprint, err := hex.DecodeString(doc.RSAFingerprint)
	if err != nil || len(fingerprint) != clusterreg.RelayFingerprintLen || doc.RSAFingerprint != strings.ToUpper(doc.RSAFingerprint) {
		return RelayIdentity{}, fmt.Errorf("the relay fingerprint %q is not %d bytes of upper-case hex", doc.RSAFingerprint, clusterreg.RelayFingerprintLen)
	}
	pub, err := hex.DecodeString(doc.Pubkey)
	if err != nil {
		return RelayIdentity{}, fmt.Errorf("the relay key is not hex: %w", err)
	}
	sig, err := hex.DecodeString(doc.Signature)
	if err != nil {
		return RelayIdentity{}, fmt.Errorf("the relay binding's signature is not hex: %w", err)
	}
	cert, err := hex.DecodeString(doc.CrossCert)
	if err != nil {
		return RelayIdentity{}, fmt.Errorf("the relay cross-certificate is not hex: %w", err)
	}
	rsaPublic, err := hex.DecodeString(doc.RSAPublicKey)
	if err != nil {
		return RelayIdentity{}, fmt.Errorf("the relay RSA public key is not hex: %w", err)
	}
	rsaProof, err := hex.DecodeString(doc.RSASignature)
	if err != nil {
		return RelayIdentity{}, fmt.Errorf("the relay RSA signature is not hex: %w", err)
	}
	if doc.Service != clusterreg.RelayService || doc.KeyType != KeyTypeEd25519 {
		return RelayIdentity{}, fmt.Errorf("the relay binding is for %s key of service %q, not an ed25519 key of service %q", doc.KeyType, doc.Service, clusterreg.RelayService)
	}
	binding := Binding{Service: doc.Service, KeyType: doc.KeyType, Pubkey: pub, Signature: sig}
	if err := Verify(binding, chainID, operator); err != nil {
		return RelayIdentity{}, fmt.Errorf("the relay binding for operator %s on chain %s: %w", operator, chainID, err)
	}
	if len(cert) != clusterreg.RelaySignatureLen || !ed25519.Verify(pub, clusterreg.RelayCrossCertMessage(nodeID, fingerprint, exit), cert) {
		return RelayIdentity{}, fmt.Errorf("the relay cross-certificate of node %q does not verify under the relay's key for exit=%v", nodeID, exit)
	}
	if err := verifyRSAProof(fingerprint, rsaPublic, rsaProof, nodeID, pub, exit); err != nil {
		return RelayIdentity{}, fmt.Errorf("the relay RSA proof of node %q: %w", nodeID, err)
	}
	return RelayIdentity{Fingerprint: fingerprint, Binding: binding, CrossCert: cert, RSAPublicKey: rsaPublic, RSASignature: rsaProof}, nil
}

// verifyRSAProof checks what the chain checks (x/relay VerifyRSAProof): the public key is the one whose
// SHA-1 is the fingerprint, in canonical DER, of a size Tor and the chain accept, and the signature is
// its own over the proof message.
func verifyRSAProof(fingerprint, publicKey, signature []byte, nodeID string, ed25519Pub []byte, exit bool) error {
	if len(publicKey) == 0 || len(publicKey) > clusterreg.RelayRSAPublicKeyMaxLen {
		return fmt.Errorf("the RSA public key is %d bytes, want 1 to %d", len(publicKey), clusterreg.RelayRSAPublicKeyMaxLen)
	}
	digest := sha1.Sum(publicKey) // #nosec G401
	if !bytes.Equal(digest[:], fingerprint) {
		return fmt.Errorf("the RSA public key does not hash to the fingerprint")
	}
	pub, err := x509.ParsePKCS1PublicKey(publicKey)
	if err != nil {
		return fmt.Errorf("the RSA public key: %w", err)
	}
	if !bytes.Equal(x509.MarshalPKCS1PublicKey(pub), publicKey) {
		return fmt.Errorf("the RSA public key is not in canonical DER")
	}
	if bits := pub.N.BitLen(); bits < clusterreg.RelayRSAMinBits || bits > clusterreg.RelayRSAMaxBits {
		return fmt.Errorf("the RSA key is %d bits, want %d to %d", bits, clusterreg.RelayRSAMinBits, clusterreg.RelayRSAMaxBits)
	}
	sum := sha256.Sum256(clusterreg.RelayRSAProofMessage(nodeID, ed25519Pub, exit))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], signature); err != nil {
		return fmt.Errorf("the signature does not verify under the RSA public key")
	}
	return nil
}
