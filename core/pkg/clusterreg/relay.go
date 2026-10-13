package clusterreg

import (
	"fmt"
)

const (
	// RegisterRelayTypeURL is the Any type URL of orama.relay.v1.MsgRegisterRelay.
	RegisterRelayTypeURL = "/orama.relay.v1.MsgRegisterRelay"
	// UpdateRelayTypeURL is the Any type URL of orama.relay.v1.MsgUpdateRelay.
	UpdateRelayTypeURL = "/orama.relay.v1.MsgUpdateRelay"

	// RelayService is the service name of the node binding that holds a Tor relay's ed25519
	// identity. x/relay reads the relay's key from the binding of this name and from no other.
	RelayService = "relay"

	// RelayFingerprintLen is the SHA-1 digest length of a Tor relay's RSA identity.
	RelayFingerprintLen = 20
	// RelaySignatureLen is the length of an ed25519 signature.
	RelaySignatureLen = 64

	// RelayRSAPublicKeyMaxLen bounds the DER of the relay's RSA identity key (chain x/relay MaxRSAPublicKeyLen).
	RelayRSAPublicKeyMaxLen = 1024
	// RelayRSAMinBits and RelayRSAMaxBits bound the size of the relay's RSA identity key (chain x/relay MinRSABits and
	// MaxRSABits). Tor's identity keys are 1024 bits.
	RelayRSAMinBits = 1024
	RelayRSAMaxBits = 4096
	// RelayRSASignatureMaxLen bounds the RSA proof: a signature is as long as the key, and a 4096-bit key is 512 bytes.
	RelayRSASignatureMaxLen = 512

	// relayCrossCertDomain separates the relay cross-certificate from every other ed25519 message
	// the same key signs, and relayRSAProofDomain the RSA proof from every other RSA message.
	// chain/x/relay/types holds the same strings (crosscert.go, rsaproof.go), and the tests in both
	// modules pin the bytes.
	relayCrossCertDomain = "orama/relay/rsa-cross-cert/v2"
	relayRSAProofDomain  = "orama/relay/rsa-proof/v2"
)

// RelayRegistration is MsgRegisterRelay and MsgUpdateRelay, which carry the same fields: the
// operator of NodeID registers (or rotates) the Tor relay that runs on it. RSAFingerprint is the
// relay's 20-byte RSA identity digest and Ed25519Signature the relay's ed25519 identity, the key the
// node binds under RelayService, signing RelayCrossCertMessage over the node id and the fingerprint.
// RSAPublicKey is the relay's RSA identity key (PKCS#1 DER, whose SHA-1 is the fingerprint) and
// RSASignature its signature over RelayRSAProofMessage: a fingerprint is public, so the chain asks
// the registrant to prove it holds the key.
type RelayRegistration struct {
	Operator         string
	NodeID           string
	RSAFingerprint   []byte
	Exit             bool
	Ed25519Signature []byte
	RSAPublicKey     []byte
	RSASignature     []byte
}

// relayExitByte is the byte that carries a relay's exit declaration in both signed messages.
func relayExitByte(exit bool) byte {
	if exit {
		return 1
	}
	return 0
}

// RelayCrossCertMessage is the byte string the relay's ed25519 identity signs to bind an RSA
// fingerprint to a node id and an exit declaration: the domain, the node id, the fingerprint and one
// exit byte (0 or 1), each after a zero byte. The exit byte keeps the proofs of a non-exit relay from
// being replayed to register or update it as an exit.
func RelayCrossCertMessage(nodeID string, rsaFingerprint []byte, exit bool) []byte {
	msg := make([]byte, 0, len(relayCrossCertDomain)+1+len(nodeID)+1+len(rsaFingerprint)+1+1)
	msg = append(msg, relayCrossCertDomain...)
	msg = append(msg, 0)
	msg = append(msg, nodeID...)
	msg = append(msg, 0)
	msg = append(msg, rsaFingerprint...)
	msg = append(msg, 0)
	return append(msg, relayExitByte(exit))
}

// RelayRSAProofMessage is the byte string the relay's RSA identity key signs (RSASSA-PKCS1-v1_5 over its
// SHA-256): the domain, the node id, the ed25519 key the node binds as its relay key and one exit byte
// (0 or 1), each after a zero byte. It binds the RSA identity to that node, that key and that exit
// declaration.
func RelayRSAProofMessage(nodeID string, ed25519Pub []byte, exit bool) []byte {
	msg := make([]byte, 0, len(relayRSAProofDomain)+1+len(nodeID)+1+len(ed25519Pub)+1+1)
	msg = append(msg, relayRSAProofDomain...)
	msg = append(msg, 0)
	msg = append(msg, nodeID...)
	msg = append(msg, 0)
	msg = append(msg, ed25519Pub...)
	msg = append(msg, 0)
	return append(msg, relayExitByte(exit))
}

// ValidateRelay checks the operator, the node id and the shapes of the fingerprint and signature.
// The chain verifies the signature itself.
func ValidateRelay(r RelayRegistration) error {
	if _, err := CanonicalAccount(r.Operator); err != nil {
		return fmt.Errorf("operator: %w", err)
	}
	if !idPattern.MatchString(r.NodeID) {
		return fmt.Errorf("node id %q must match %s", r.NodeID, idPattern.String())
	}
	if len(r.RSAFingerprint) != RelayFingerprintLen {
		return fmt.Errorf("rsa fingerprint is %d bytes, want %d", len(r.RSAFingerprint), RelayFingerprintLen)
	}
	if len(r.Ed25519Signature) != RelaySignatureLen {
		return fmt.Errorf("ed25519 signature is %d bytes, want %d", len(r.Ed25519Signature), RelaySignatureLen)
	}
	if n := len(r.RSAPublicKey); n == 0 || n > RelayRSAPublicKeyMaxLen {
		return fmt.Errorf("rsa public key is %d bytes, want 1 to %d", n, RelayRSAPublicKeyMaxLen)
	}
	if n := len(r.RSASignature); n == 0 || n > RelayRSASignatureMaxLen {
		return fmt.Errorf("rsa signature is %d bytes, want 1 to %d", n, RelayRSASignatureMaxLen)
	}
	return nil
}

// EncodeRegisterRelay is the protobuf orama.relay.v1.MsgRegisterRelay. Field numbers match
// chain/x/relay/types/tx.pb.go; exit, field 4, is written only when set, as proto3 omits a false bool.
func EncodeRegisterRelay(r RelayRegistration) []byte {
	out := appendStringField(nil, 1, r.Operator)
	out = appendStringField(out, 2, r.NodeID)
	out = appendBytesField(out, 3, r.RSAFingerprint)
	if r.Exit {
		out = appendUvarintField(out, 4, 1)
	}
	out = appendBytesField(out, 5, r.Ed25519Signature)
	out = appendBytesField(out, 6, r.RSAPublicKey)
	return appendBytesField(out, 7, r.RSASignature)
}

// EncodeUpdateRelay is the protobuf orama.relay.v1.MsgUpdateRelay, which has MsgRegisterRelay's fields.
func EncodeUpdateRelay(r RelayRegistration) []byte {
	return EncodeRegisterRelay(r)
}
