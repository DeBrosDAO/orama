package types

import "crypto/ed25519"

// crossCertDomain separates relay RSA cross-cert signatures from other
// ed25519 messages the same identity might sign. v2 adds the exit
// declaration to the signed bytes; a v1 signature never verifies.
const crossCertDomain = "orama/relay/rsa-cross-cert/v2"

// exitByte is the single byte that carries a relay's exit declaration in the
// cross-certificate and the RSA proof.
func exitByte(exit bool) byte {
	if exit {
		return 1
	}
	return 0
}

// CrossCertMessage is the byte string the relay's ed25519 identity signs to
// bind an RSA fingerprint to a node id and to an exit declaration: the domain,
// the node id, the fingerprint and one byte (0 non-exit, 1 exit), each after a
// zero byte. Binding exit means a non-exit relay's proofs cannot be replayed
// to register or update it as an exit.
func CrossCertMessage(nodeID string, rsaFingerprint []byte, exit bool) []byte {
	msg := make([]byte, 0, len(crossCertDomain)+1+len(nodeID)+1+len(rsaFingerprint)+1+1)
	msg = append(msg, crossCertDomain...)
	msg = append(msg, 0)
	msg = append(msg, nodeID...)
	msg = append(msg, 0)
	msg = append(msg, rsaFingerprint...)
	msg = append(msg, 0)
	return append(msg, exitByte(exit))
}

// VerifyCrossCert reports whether signature is the ed25519 identity's
// signature over CrossCertMessage. A wrong key, a wrong fingerprint, a wrong
// exit declaration, or a truncated signature returns false.
func VerifyCrossCert(ed25519Pub []byte, nodeID string, rsaFingerprint []byte, exit bool, signature []byte) bool {
	if len(ed25519Pub) != Ed25519PubLen || len(signature) != Ed25519SigLen {
		return false
	}
	return ed25519.Verify(ed25519Pub, CrossCertMessage(nodeID, rsaFingerprint, exit), signature)
}
