package wallet

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"

	"github.com/mr-tron/base58"
)

// Solana is an Ed25519 keypair whose address is the base58 public key. The
// gateway verifies a SIWS signature as raw Ed25519 over the message bytes,
// standard base64 encoded.
type Solana struct {
	priv ed25519.PrivateKey
}

// NewSolana generates a wallet.
func NewSolana() (*Solana, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate a Solana test wallet: %w", err)
	}
	return &Solana{priv: priv}, nil
}

// Address is the base58 public key.
func (w *Solana) Address() string {
	return base58.Encode(w.priv.Public().(ed25519.PublicKey))
}

// Sign returns the standard-base64 Ed25519 signature of message.
func (w *Solana) Sign(message string) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(w.priv, []byte(message)))
}
