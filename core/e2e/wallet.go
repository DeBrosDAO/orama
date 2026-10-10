//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

// A wallet the suite can actually sign with.
//
// The suite used to make a wallet address up out of a namespace name and a
// timestamp and post it to /v1/auth/simple-key, which handed back a key for any
// address that asked. That endpoint is gone — it was how anyone could mint an
// unscoped admin key for any namespace — and with it went the suite's only way
// in, because an address nobody holds the key for cannot sign anything.
//
// This is a real secp256k1 keypair per test wallet. It signs the challenge the
// gateway issues, exactly as RootWallet does, so the suite exercises the path
// real callers use rather than one built for it.

// testWallet is a keypair and its address.
type testWallet struct {
	key     *ecdsa.PrivateKey
	address string
}

// newTestWallet generates a wallet.
func newTestWallet() (*testWallet, error) {
	key, err := ethcrypto.GenerateKey()
	if err != nil {
		return nil, fmt.Errorf("failed to generate a test wallet: %w", err)
	}
	return &testWallet{key: key, address: ethcrypto.PubkeyToAddress(key.PublicKey).Hex()}, nil
}

// sign returns the EIP-191 personal_sign signature of message, hex-encoded with
// the recovery byte in the 27/28 form the gateway accepts.
func (w *testWallet) sign(message string) (string, error) {
	msg := []byte(message)
	prefix := []byte("\x19Ethereum Signed Message:\n" + strconv.Itoa(len(msg)))
	hash := ethcrypto.Keccak256(prefix, msg)

	sig, err := ethcrypto.Sign(hash, w.key)
	if err != nil {
		return "", fmt.Errorf("failed to sign the challenge: %w", err)
	}
	sig[64] += 27
	return "0x" + hex.EncodeToString(sig), nil
}

// challenge asks the gateway for a message to sign and returns it verbatim. The
// message must go back exactly as it came: it carries the nonce, the domain and
// the times the gateway checks.
func (w *testWallet) challenge(gatewayURL, namespace string) (string, error) {
	body, _ := json.Marshal(map[string]string{"wallet": w.address, "namespace": namespace})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		gatewayURL+"/v1/auth/challenge", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("failed to build the challenge request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := NewHTTPClient(10 * time.Second).Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to reach the gateway for a challenge: %w", err)
	}
	defer resp.Body.Close()

	var out struct {
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("failed to decode the challenge: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("the gateway refused to issue a challenge (%d): %s", resp.StatusCode, out.Error)
	}
	if strings.TrimSpace(out.Message) == "" {
		return "", fmt.Errorf("the gateway issued an empty challenge")
	}
	return out.Message, nil
}
