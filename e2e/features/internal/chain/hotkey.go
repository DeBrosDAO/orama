//go:build e2e_fleet

package chain

import (
	"crypto/sha256"
	"fmt"
	"testing"

	"golang.org/x/crypto/ripemd160" //nolint:staticcheck // the account address hash of a Cosmos secp256k1 key
)

// HotKeyService is the service name of the binding a node's hot key signs to
// prove it holds itself (x/nodes/types/hotkey.go): MsgRegisterNode and a
// MsgUpdateNode that changes the hot key are refused without it.
const HotKeyService = "hot-key"

// HotKeyBinding makes a fresh secp256k1 hot key, signs the binding statement
// for chainID and operator with it, and returns the key's account address and
// the binding. The private key is dropped: the hot key never signs anything
// here, so the fee-only balance MsgFundHotKey gives it is only ever read.
func HotKeyBinding(t testing.TB, chainID, operator string) (string, Binding) {
	t.Helper()
	b := Secp256k1Binding(t, chainID, operator, HotKeyService)
	addr, err := AddressOfPubKey(b.Pubkey)
	if err != nil {
		t.Fatalf("failed to derive the hot key's address: %v", err)
	}
	return addr, b
}

// AddressOfPubKey is the account address of a compressed secp256k1 public
// key: RIPEMD160(SHA256(pubkey)) under the orama prefix.
func AddressOfPubKey(pub []byte) (string, error) {
	sum := sha256.Sum256(pub)
	h := ripemd160.New()
	if _, err := h.Write(sum[:]); err != nil {
		return "", fmt.Errorf("failed to hash the public key: %w", err)
	}
	return Bech32(AccountPrefix, h.Sum(nil))
}
