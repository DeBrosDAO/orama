package auth

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/crypto"
)

// VerifyEVM verifies an Ethereum personal_sign signature.
func VerifyEVM(wallet, message, signatureHex string) error {
	// Remove 0x prefix if present
	sigHex := strings.TrimPrefix(signatureHex, "0x")
	sigBytes, err := hex.DecodeString(sigHex)
	if err != nil {
		return fmt.Errorf("failed to decode signature hex: %w", err)
	}

	if len(sigBytes) != 65 {
		return fmt.Errorf("invalid signature length: got %d, want 65", len(sigBytes))
	}

	// Ethereum personal_sign prefix
	prefixed := fmt.Sprintf("\x19Ethereum Signed Message:\n%d%s", len(message), message)
	hash := crypto.Keccak256Hash([]byte(prefixed))

	// Fix recovery ID: some wallets return v=27/28, need v=0/1
	if sigBytes[64] >= 27 {
		sigBytes[64] -= 27
	}

	pubKeyBytes, err := crypto.Ecrecover(hash.Bytes(), sigBytes)
	if err != nil {
		return fmt.Errorf("ecrecover failed: %w", err)
	}

	pubKey, err := crypto.UnmarshalPubkey(pubKeyBytes)
	if err != nil {
		return fmt.Errorf("failed to unmarshal public key: %w", err)
	}

	recoveredAddr := crypto.PubkeyToAddress(*pubKey)
	expectedAddr := strings.ToLower(strings.TrimPrefix(wallet, "0x"))
	recoveredHex := strings.ToLower(recoveredAddr.Hex()[2:])

	if recoveredHex != expectedAddr {
		return fmt.Errorf("signature verification failed: recovered %s, expected %s", recoveredHex, expectedAddr)
	}

	return nil
}
