//go:build e2e_fleet

package chain

import (
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1" // #nosec G505 -- a Tor relay fingerprint is SHA-1 by definition
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"testing"
)

// Relay identity sizes (chain/x/relay/types/limits.go, rsaproof.go) and the domains of the
// cross-certificate and the RSA proof (chain/x/relay/types/crosscert.go, rsaproof.go).
const (
	RelayRSAFingerprintLen = 20
	RelayRSABits           = 1024
	RelayInputsRootLen     = 32
	RelayCrossCertDomain   = "orama/relay/rsa-cross-cert/v2"
	RelayRSAProofDomain    = "orama/relay/rsa-proof/v2"
)

// RelayRSAIdentity is a relay's RSA identity: its key, the PKCS#1 DER of the public half and the SHA-1 of
// that DER, which is the fingerprint Tor and the chain name the relay by.
type RelayRSAIdentity struct {
	Key         *rsa.PrivateKey
	DER         []byte
	Fingerprint []byte
}

// NewRelayRSAIdentity generates a fresh relay RSA identity.
func NewRelayRSAIdentity(t *testing.T) RelayRSAIdentity {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, RelayRSABits)
	if err != nil {
		t.Fatal(err)
	}
	der := x509.MarshalPKCS1PublicKey(&key.PublicKey)
	sum := sha1.Sum(der) // #nosec G401
	return RelayRSAIdentity{Key: key, DER: der, Fingerprint: sum[:]}
}

// relayExitByte is the exit declaration both signed messages end with.
func relayExitByte(exit bool) byte {
	if exit {
		return 1
	}
	return 0
}

// relayCrossCertMessage is domain||0||node id||0||fingerprint||0||exit (types.CrossCertMessage).
func relayCrossCertMessage(nodeID string, fp []byte, exit bool) []byte {
	msg := append(append(append(append([]byte(RelayCrossCertDomain), 0), nodeID...), 0), fp...)
	return append(msg, 0, relayExitByte(exit))
}

// relayProofMessage is domain||0||node id||0||the relay's ed25519 key||0||exit (types.RSAProofMessage).
func relayProofMessage(nodeID string, ed25519Pub ed25519.PublicKey, exit bool) []byte {
	msg := append(append(append(append([]byte(RelayRSAProofDomain), 0), nodeID...), 0), ed25519Pub...)
	return append(msg, 0, relayExitByte(exit))
}

// RelayCrossCert is the relay's ed25519 signature over its cross-certificate message.
func RelayCrossCert(priv ed25519.PrivateKey, nodeID string, fp []byte, exit bool) []byte {
	return ed25519.Sign(priv, relayCrossCertMessage(nodeID, fp, exit))
}

// Proof is the RSA identity's signature (PKCS#1 v1.5 over SHA-256) over the proof message: proof that
// whoever registers the fingerprint holds its key.
func (r RelayRSAIdentity) Proof(t *testing.T, nodeID string, ed25519Pub ed25519.PublicKey, exit bool) []byte {
	t.Helper()
	sum := sha256.Sum256(relayProofMessage(nodeID, ed25519Pub, exit))
	sig, err := rsa.SignPKCS1v15(rand.Reader, r.Key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return sig
}

// RelayRegistration holds the fields of MsgRegisterRelay and MsgUpdateRelay, which are the same.
type RelayRegistration struct {
	Operator, NodeID string
	Fingerprint      []byte
	Exit             bool
	Ed25519Sig       []byte
	RSAPublicKey     []byte
	RSASig           []byte
}

// ValidRelay is a non-exit registration of node nodeID for the RSA identity id, signed the way the chain checks it.
func ValidRelay(t *testing.T, operator, nodeID string, id RelayRSAIdentity, priv ed25519.PrivateKey) RelayRegistration {
	t.Helper()
	return ValidRelayExit(t, operator, nodeID, id, priv, false)
}

// ValidRelayExit is ValidRelay for a relay that declares exit as given; both signatures cover it.
func ValidRelayExit(t *testing.T, operator, nodeID string, id RelayRSAIdentity, priv ed25519.PrivateKey, exit bool) RelayRegistration {
	t.Helper()
	return RelayRegistration{
		Operator: operator, NodeID: nodeID, Fingerprint: id.Fingerprint, Exit: exit,
		Ed25519Sig: RelayCrossCert(priv, nodeID, id.Fingerprint, exit), RSAPublicKey: id.DER,
		RSASig: id.Proof(t, nodeID, priv.Public().(ed25519.PublicKey), exit),
	}
}

// Msg is the registration as a message of the given type URL.
func (f RelayRegistration) Msg(typeURL string) Msg {
	b64 := base64.StdEncoding.EncodeToString
	return NewMsg(typeURL, map[string]any{
		"operator": f.Operator, "node_id": f.NodeID, "rsa_fingerprint": b64(f.Fingerprint), "exit": f.Exit,
		"ed25519_signature": b64(f.Ed25519Sig), "rsa_public_key": b64(f.RSAPublicKey), "rsa_signature": b64(f.RSASig),
	})
}

// Register is the registration as MsgRegisterRelay.
func (f RelayRegistration) Register() Msg { return f.Msg("/orama.relay.v1.MsgRegisterRelay") }

// Update is the registration as MsgUpdateRelay.
func (f RelayRegistration) Update() Msg { return f.Msg("/orama.relay.v1.MsgUpdateRelay") }
