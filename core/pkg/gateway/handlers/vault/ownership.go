package vault

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// pullMaxSkewSeconds bounds how far a pull request timestamp may be from the
// server clock. A replay within this window only re-fetches ciphertext the
// attacker cannot decrypt, so a small window is safe and avoids stateful nonces.
const pullMaxSkewSeconds int64 = 120

// identityMatchesPubkey reports whether identityHex == SHA-256(pubkey) in hex.
// The identity is public (not a secret), so a plain case-insensitive compare is
// fine here.
func identityMatchesPubkey(identityHex string, pubkey []byte) bool {
	sum := sha256.Sum256(pubkey)
	return strings.EqualFold(identityHex, hex.EncodeToString(sum[:]))
}

// verifyPush checks the caller owns the identity for a push: identity ==
// SHA-256(pubkey) and a valid Ed25519 signature over the canonical push message.
// The message binds the monotonic version, so a captured signature cannot be
// reused for a different version. Must match the guardian (Zig) and client.
func verifyPush(identityHex string, version uint64, pubkeyHex, sigHex string) bool {
	pubkey, err := hex.DecodeString(pubkeyHex)
	if err != nil || len(pubkey) != ed25519.PublicKeySize {
		return false
	}
	sig, err := hex.DecodeString(sigHex)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return false
	}
	if !identityMatchesPubkey(identityHex, pubkey) {
		return false
	}
	msg := fmt.Sprintf("vault-push-v1:%s:%d", identityHex, version)
	return ed25519.Verify(ed25519.PublicKey(pubkey), []byte(msg), sig)
}

// verifyPull checks the caller owns the identity for a pull: identity ==
// SHA-256(pubkey), the timestamp is within the skew window, and a valid Ed25519
// signature over the canonical pull message. This is what closes the
// password-oracle — an attacker who only knows the identity cannot read the blob.
func verifyPull(identityHex string, timestamp, now int64, pubkeyHex, sigHex string) bool {
	diff := now - timestamp
	if diff > pullMaxSkewSeconds || diff < -pullMaxSkewSeconds {
		return false
	}
	pubkey, err := hex.DecodeString(pubkeyHex)
	if err != nil || len(pubkey) != ed25519.PublicKeySize {
		return false
	}
	sig, err := hex.DecodeString(sigHex)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return false
	}
	if !identityMatchesPubkey(identityHex, pubkey) {
		return false
	}
	msg := fmt.Sprintf("vault-pull-v1:%s:%d", identityHex, timestamp)
	return ed25519.Verify(ed25519.PublicKey(pubkey), []byte(msg), sig)
}
