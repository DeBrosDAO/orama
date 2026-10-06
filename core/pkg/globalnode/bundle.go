package globalnode

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/nsbackup"
)

// bundleKind names the plaintext inside a migration bundle.
const bundleKind = "orama-validator-migrate-v1"

// Bundle is what moves a validator key to another host: the key and, for a
// migration, the state the key last signed on the source. A bundle made from
// a key backup (Reseal) has no state: nobody knows what a lost host signed.
type Bundle struct {
	Kind  string          `json:"kind"`
	Key   json.RawMessage `json:"key"`
	State json.RawMessage `json:"state,omitempty"`
}

// SealBundle checks key and state and seals them to recipient with the
// nsbackup ORBK seal. state may be nil.
func SealBundle(recipient *[32]byte, key, state []byte) ([]byte, error) {
	if _, err := ValidatorKeyPubKey(key); err != nil {
		return nil, err
	}
	b := Bundle{Kind: bundleKind, Key: key}
	if state != nil {
		if _, err := ParseSignState(state); err != nil {
			return nil, err
		}
		b.State = state
	}
	plain, err := json.Marshal(b)
	if err != nil {
		return nil, fmt.Errorf("encode the migration bundle: %w", err)
	}
	return nsbackup.Seal(recipient, plain)
}

// OpenBundle opens a sealed bundle with the recipient's private key and
// checks what is inside.
func OpenBundle(priv *[32]byte, blob []byte) (Bundle, error) {
	plain, err := nsbackup.Open(priv, blob)
	if err != nil {
		return Bundle{}, fmt.Errorf("open the migration bundle: %w", err)
	}
	var b Bundle
	if err := json.Unmarshal(plain, &b); err != nil || b.Kind != bundleKind {
		return Bundle{}, fmt.Errorf("the sealed file is not a validator migration bundle")
	}
	if _, err := ValidatorKeyPubKey(b.Key); err != nil {
		return Bundle{}, err
	}
	if b.State != nil {
		if _, err := ParseSignState(b.State); err != nil {
			return Bundle{}, err
		}
	}
	return b, nil
}

// Reseal turns a key backup (ExportKey's output, sealed to the operator's
// key) into a migration bundle for a new host's recipient key, on the
// operator's machine. The bundle carries no state.
func Reseal(operatorPriv, recipient *[32]byte, backup []byte) ([]byte, error) {
	key, err := nsbackup.Open(operatorPriv, backup)
	if err != nil {
		return nil, fmt.Errorf("open the key backup: %w", err)
	}
	return SealBundle(recipient, key, nil)
}

// ParseX25519Hex reads a 32-byte X25519 key written as 64 hex characters, as
// `orama namespace backup-seal` takes it. Surrounding whitespace is ignored.
func ParseX25519Hex(s string) (*[32]byte, error) {
	raw, err := hex.DecodeString(strings.TrimSpace(s))
	if err != nil || len(raw) != 32 {
		return nil, fmt.Errorf("an X25519 key is 64 hex characters")
	}
	var key [32]byte
	copy(key[:], raw)
	return &key, nil
}
