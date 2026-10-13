package wallet

import (
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth/siw"
)

// The sign-in message grammar is the gateway's own package, used directly: a
// second hand-written parser would drift, and a test that builds a forged
// message must produce exactly the bytes a real wallet would be shown.

// SIWEMessage is one parsed Sign-In-With message (EIP-4361 or SIWS).
type SIWEMessage = siw.Message

// Chains a message can be issued for.
const (
	ChainEthereum = siw.Ethereum
	ChainSolana   = siw.Solana
)

// ParseSIWE parses a challenge message exactly as the gateway will.
func ParseSIWE(text string) (*SIWEMessage, error) {
	m, err := siw.Parse(text)
	if err != nil {
		return nil, fmt.Errorf("failed to parse sign-in message: %w", err)
	}
	return m, nil
}

// Mutate parses a challenge, applies change and renders it again. Negative
// tests use it to sign a message the gateway did not issue (another domain,
// an expired time, a different nonce) with a real key.
func Mutate(text string, change func(*SIWEMessage)) (string, error) {
	m, err := ParseSIWE(text)
	if err != nil {
		return "", err
	}
	change(m)
	out, err := m.Render()
	if err != nil {
		return "", fmt.Errorf("failed to render mutated sign-in message: %w", err)
	}
	return out, nil
}
