package types

import "crypto/ed25519"

// crossCertDomain separates relay RSA cross-cert signatures from other
// ed25519 messages the same identity might sign.
const crossCertDomain = "orama/relay/rsa-cross-cert/v1"

// CrossCertMessage is the byte string the relay's ed25519 identity signs to
// bind an RSA fingerprint to a node id.
func CrossCertMessage(nodeID string, rsaFingerprint []byte) []byte {
	msg := make([]byte, 0, len(crossCertDomain)+1+len(nodeID)+1+len(rsaFingerprint))
	msg = append(msg, crossCertDomain...)
	msg = append(msg, 0)
	msg = append(msg, nodeID...)
	msg = append(msg, 0)
	msg = append(msg, rsaFingerprint...)
	return msg
}

// VerifyCrossCert reports whether signature is the ed25519 identity's
// signature over CrossCertMessage. A wrong key, a wrong fingerprint, or a
// truncated signature returns false.
func VerifyCrossCert(ed25519Pub []byte, nodeID string, rsaFingerprint, signature []byte) bool {
	if len(ed25519Pub) != Ed25519PubLen || len(signature) != Ed25519SigLen {
		return false
	}
	return ed25519.Verify(ed25519Pub, CrossCertMessage(nodeID, rsaFingerprint), signature)
}
