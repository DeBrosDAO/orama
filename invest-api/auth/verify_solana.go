package auth

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"

	"github.com/mr-tron/base58"
)

// VerifySolana verifies a Solana wallet signature.
// The signature should be base58 or base64 encoded.
// The wallet is the base58-encoded public key.
func VerifySolana(wallet, message, signature string) error {
	pubKeyBytes, err := base58.Decode(wallet)
	if err != nil {
		return fmt.Errorf("invalid wallet address: %w", err)
	}

	if len(pubKeyBytes) != ed25519.PublicKeySize {
		return fmt.Errorf("invalid public key length: got %d, want %d", len(pubKeyBytes), ed25519.PublicKeySize)
	}

	// Try base58 first (Phantom default), then base64
	sigBytes, err := base58.Decode(signature)
	if err != nil || len(sigBytes) != ed25519.SignatureSize {
		sigBytes, err = base64.StdEncoding.DecodeString(signature)
		if err != nil {
			return fmt.Errorf("failed to decode signature: %w", err)
		}
	}

	if len(sigBytes) != ed25519.SignatureSize {
		return fmt.Errorf("invalid signature length: got %d, want %d", len(sigBytes), ed25519.SignatureSize)
	}

	if !ed25519.Verify(pubKeyBytes, []byte(message), sigBytes) {
		return fmt.Errorf("signature verification failed")
	}

	return nil
}
