package types

import (
	"crypto/ed25519"
	"encoding/hex"
	"fmt"

	secp256k1 "github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
)

// BindingPrefix is the domain separator on every service-key binding
// (plans/open-network.md "Keys").
const BindingPrefix = "orama-global-bind-v1"

// BindingSignBytes is the exact ASCII message a service key signs:
// orama-global-bind-v1|chain-id|operator|service|hex(pubkey).
// operator is the canonical bech32 account string. pubkey hex is lowercase.
func BindingSignBytes(chainID, operator, service string, pubkey []byte) []byte {
	return []byte(fmt.Sprintf("%s|%s|%s|%s|%s", BindingPrefix, chainID, operator, service, hex.EncodeToString(pubkey)))
}

// VerifyBinding checks one binding against the chain id and operator it
// claims. secp256k1 verification uses the Cosmos SHA-256 digest.
// ed25519 verification accepts a standard 64-byte signature, which is what
// Tor's expanded ed25519 secret produces.
func VerifyBinding(chainID, operator string, binding Binding) error {
	if chainID == "" {
		return fmt.Errorf("chain id is empty")
	}
	if err := ValidateBindingShape(binding); err != nil {
		return err
	}
	msg := BindingSignBytes(chainID, operator, binding.Service, binding.Pubkey)
	switch binding.KeyType {
	case KeyTypeSecp256k1:
		pub := &secp256k1.PubKey{Key: binding.Pubkey}
		if !pub.VerifySignature(msg, binding.Signature) {
			return fmt.Errorf("service %q: %w", binding.Service, ErrInvalidBinding)
		}
	case KeyTypeEd25519:
		if !ed25519.Verify(ed25519.PublicKey(binding.Pubkey), msg, binding.Signature) {
			return fmt.Errorf("service %q: %w", binding.Service, ErrInvalidBinding)
		}
	default:
		return fmt.Errorf("service %q: unknown key type %s", binding.Service, binding.KeyType)
	}
	return nil
}
