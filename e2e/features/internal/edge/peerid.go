//go:build e2e_fleet

package edge

import (
	"crypto/rand"
	"fmt"

	"github.com/mr-tron/base58"
)

// An Ed25519 libp2p peer id is the identity multihash of the protobuf-encoded
// public key, base58btc (go-libp2p core/peer IDFromPublicKey; core/crypto/pb
// KeyType Ed25519 = 1). Built here so a test can name a well-formed peer that
// no machine holds the key of, without a direct libp2p dependency.
const (
	ed25519KeyLen      = 32
	pbTypeTag          = 0x08 // field 1 (Type), varint
	pbKeyTypeEd25519   = 0x01
	pbDataTag          = 0x12 // field 2 (Data), length-delimited
	multihashIdentity  = 0x00
	encodedPublicKeyLn = 4 + ed25519KeyLen
)

// UnheldPeerID is a well-formed Ed25519 peer id over a random public key: it
// parses as a peer id everywhere, and no node can ever sign as it.
func UnheldPeerID() (string, error) {
	key := make([]byte, ed25519KeyLen)
	if _, err := rand.Read(key); err != nil {
		return "", fmt.Errorf("failed to draw a random public key for a peer id: %w", err)
	}
	encoded := append([]byte{pbTypeTag, pbKeyTypeEd25519, pbDataTag, ed25519KeyLen}, key...)
	mh := append([]byte{multihashIdentity, encodedPublicKeyLn}, encoded...)
	return base58.Encode(mh), nil
}
