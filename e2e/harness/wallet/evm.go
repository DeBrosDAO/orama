// Package wallet holds the throwaway identities feature tests sign with: EVM
// wallets (EIP-191 personal_sign over the gateway's SIWE challenge), Solana
// wallets (SIWS), and device keys (Ed25519, ES256) with their RFC 7638 ids and
// device proofs. Every key is generated in process per test and never stored.
package wallet

import (
	"crypto/ecdsa"
	"encoding/hex"
	"fmt"
	"strconv"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

// personalSignPrefix is EIP-191's version 0x45 prefix.
const personalSignPrefix = "\x19Ethereum Signed Message:\n"

// recoveryOffset turns go-ethereum's 0/1 recovery id into the 27/28 form
// wallets emit and the gateway accepts.
const recoveryOffset = 27

// EVM is a secp256k1 keypair and its checksummed address.
type EVM struct {
	key     *ecdsa.PrivateKey
	address string
}

// NewEVM generates a wallet.
func NewEVM() (*EVM, error) {
	key, err := ethcrypto.GenerateKey()
	if err != nil {
		return nil, fmt.Errorf("failed to generate an EVM test wallet: %w", err)
	}
	return &EVM{key: key, address: ethcrypto.PubkeyToAddress(key.PublicKey).Hex()}, nil
}

// EVMFromHex loads a wallet from a hex private key (with or without 0x). For
// fixed test vectors only; run wallets are always generated.
func EVMFromHex(hexKey string) (*EVM, error) {
	if len(hexKey) > 1 && hexKey[:2] == "0x" {
		hexKey = hexKey[2:]
	}
	key, err := ethcrypto.HexToECDSA(hexKey)
	if err != nil {
		return nil, fmt.Errorf("failed to parse EVM private key: %w", err)
	}
	return &EVM{key: key, address: ethcrypto.PubkeyToAddress(key.PublicKey).Hex()}, nil
}

// Address is the EIP-55 checksummed address.
func (w *EVM) Address() string { return w.address }

// Sign returns the EIP-191 personal_sign signature of message: 0x-prefixed hex,
// 65 bytes, recovery byte 27/28.
func (w *EVM) Sign(message string) (string, error) {
	sig, err := ethcrypto.Sign(PersonalHash(message), w.key)
	if err != nil {
		return "", fmt.Errorf("failed to sign with %s: %w", w.address, err)
	}
	sig[64] += recoveryOffset
	return "0x" + hex.EncodeToString(sig), nil
}

// PersonalHash is keccak256 of the EIP-191 prefixed message.
func PersonalHash(message string) []byte {
	prefix := personalSignPrefix + strconv.Itoa(len(message))
	return ethcrypto.Keccak256([]byte(prefix), []byte(message))
}

// RecoverAddress returns the address that produced sig over message, for tests
// that check a signature the harness did not make.
func RecoverAddress(message, sig string) (string, error) {
	if len(sig) > 1 && sig[:2] == "0x" {
		sig = sig[2:]
	}
	raw, err := hex.DecodeString(sig)
	if err != nil {
		return "", fmt.Errorf("failed to decode signature: %w", err)
	}
	if len(raw) != 65 {
		return "", fmt.Errorf("signature is %d bytes, want 65", len(raw))
	}
	if raw[64] >= recoveryOffset {
		raw[64] -= recoveryOffset
	}
	pub, err := ethcrypto.SigToPub(PersonalHash(message), raw)
	if err != nil {
		return "", fmt.Errorf("failed to recover signer: %w", err)
	}
	return ethcrypto.PubkeyToAddress(*pub).Hex(), nil
}
