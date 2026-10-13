package types

import (
	"fmt"

	secp256k1 "github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

// HotKeyService is the service name of the binding that proves a node's hot key. The hot key signs
// the ordinary binding statement (orama-global-bind-v1|chain-id|operator|hot-key|hex(pubkey)) with
// its own secp256k1 key, so an operator cannot name an address it does not control, and cannot
// name another operator's, as its node's hot key: the chain would otherwise let MsgFundHotKey and
// the hot-key-only storage messages point at an arbitrary third party.
const HotKeyService = "hot-key"

// HotKeyAddress is the account address of a hot-key binding's secp256k1 public key.
func HotKeyAddress(binding Binding) (string, error) {
	if binding.KeyType != KeyTypeSecp256k1 {
		return "", fmt.Errorf("the %q binding must be a secp256k1 key, got %s", HotKeyService, binding.KeyType)
	}
	if len(binding.Pubkey) != Secp256k1PubKeyLen {
		return "", fmt.Errorf("the %q binding pubkey must be %d bytes, got %d", HotKeyService, Secp256k1PubKeyLen, len(binding.Pubkey))
	}
	pub := &secp256k1.PubKey{Key: binding.Pubkey}
	return sdk.AccAddress(pub.Address()).String(), nil
}

// CheckHotKeyBinding verifies that bindings carry exactly one hot-key binding and that its key is
// hotKey's. It checks shape and identity only; the signature is checked with every other binding.
func CheckHotKeyBinding(hotKey string, bindings []Binding) error {
	var found *Binding
	for i := range bindings {
		if bindings[i].Service != HotKeyService {
			continue
		}
		if found != nil {
			return fmt.Errorf("two %q bindings: %w", HotKeyService, ErrHotKey)
		}
		found = &bindings[i]
	}
	if found == nil {
		return fmt.Errorf("a %q binding signed by the hot key is required: %w", HotKeyService, ErrHotKey)
	}
	addr, err := HotKeyAddress(*found)
	if err != nil {
		return fmt.Errorf("%w: %w", err, ErrHotKey)
	}
	if addr != hotKey {
		return fmt.Errorf("the %q binding is for %s, not the hot key %s: %w", HotKeyService, addr, hotKey, ErrHotKey)
	}
	return nil
}
