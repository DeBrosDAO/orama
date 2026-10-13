package chainfaucet

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"

	secp256k1 "github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"

	"github.com/DeBrosOfficial/network/pkg/clusterreg"
	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/rwagent"
)

const (
	// KeyFileName is the faucet key's file name.
	KeyFileName = "chain-faucet.key"
	// DefaultKeyFile is where `orama maint faucet init` puts the key and where node.yaml's
	// chain.faucet.key_file points unless it says otherwise.
	DefaultKeyFile = constants.ChainFaucetKeyFile
	// GatewayUser owns the key: the account the gateway runs as.
	GatewayUser = "orama"

	// KeyFileMode is the key file's mode: its owner reads and writes it, nobody else.
	KeyFileMode = 0o600
	// secretBytes is the size of a secp256k1 secret; the file holds it as hex and a newline.
	secretBytes = 32
	// maxKeyFileBytes bounds what is read from a key file.
	maxKeyFileBytes = 4 * (2*secretBytes + 1)
)

// Key is the faucet account's secp256k1 key. It is an onchain.Signer: the faucet service signs
// with it in process, where an operator's transactions are signed by a RootWallet.
type Key struct {
	priv    *secp256k1.PrivateKey
	pub     []byte
	address string
}

// NewKey returns a fresh random key.
func NewKey() (*Key, error) {
	var secret [secretBytes]byte
	if _, err := io.ReadFull(rand.Reader, secret[:]); err != nil {
		return nil, fmt.Errorf("read randomness for a faucet key: %w", err)
	}
	return keyFromSecret(secret[:])
}

func keyFromSecret(secret []byte) (*Key, error) {
	if len(secret) != secretBytes {
		return nil, fmt.Errorf("a faucet key is %d bytes, got %d", secretBytes, len(secret))
	}
	priv := secp256k1.PrivKeyFromBytes(secret)
	if priv.Key.IsZero() {
		return nil, errors.New("a faucet key cannot be zero")
	}
	pub := priv.PubKey().SerializeCompressed()
	address, err := clusterreg.AccountAddressOf(pub)
	if err != nil {
		return nil, fmt.Errorf("derive the faucet account: %w", err)
	}
	return &Key{priv: priv, pub: pub, address: address}, nil
}

// Address is the faucet account: the address to fund.
func (k *Key) Address() string { return k.address }

// OramaAccount is the account the key signs for.
func (k *Key) OramaAccount(context.Context) (*rwagent.OramaAccount, error) {
	return &rwagent.OramaAccount{Address: k.address, PubKey: append([]byte(nil), k.pub...)}, nil
}

// SignOramaTx signs a SIGN_MODE_DIRECT SignDoc: 64 bytes r||s, s low, over its SHA-256.
func (k *Key) SignOramaTx(_ context.Context, signDoc []byte) (*rwagent.OramaTxSignature, error) {
	sum := sha256.Sum256(signDoc)
	compact := ecdsa.SignCompact(k.priv, sum[:], false)
	return &rwagent.OramaTxSignature{Signature: compact[1:], PubKey: append([]byte(nil), k.pub...), Address: k.address}, nil
}

// encode is the file's content: the secret in hex and a newline.
func (k *Key) encode() []byte {
	return []byte(hex.EncodeToString(k.priv.Serialize()) + "\n")
}

// ParseKey reads a key file's content.
func ParseKey(content []byte) (*Key, error) {
	secret, err := hex.DecodeString(strings.TrimSpace(string(content)))
	if err != nil {
		return nil, errors.New("the faucet key file is not a hex secret")
	}
	return keyFromSecret(secret)
}

// LoadKey reads the key file at path. It refuses a file the gateway should not trust with a
// secret: one that is not a regular file (a symlink could point anywhere), is readable or
// writable by anyone but its owner, or is owned by another account than the one running. The
// checks are made on the file that was opened, not on its name, so a file swapped in between
// cannot pass them for another.
func LoadKey(path string) (*Key, error) {
	// O_NONBLOCK so that a FIFO put in the key's place fails the checks instead of holding the
	// gateway's start; it does nothing to a regular file.
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if errors.Is(err, syscall.ELOOP) {
		return nil, fmt.Errorf("faucet key file %s is a symlink: a symlink is not trusted with a secret, point key_file at the file itself", path)
	}
	if err != nil {
		return nil, fmt.Errorf("faucet key file %s: %w (create it with `orama maint faucet init`)", path, err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("faucet key file %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("faucet key file %s is not a regular file (%s)", path, info.Mode().Type())
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return nil, fmt.Errorf("faucet key file %s has mode %#o: anyone but its owner could read the key; run chmod %#o %s", path, perm, KeyFileMode, path)
	}
	if err := requireOwnedByUs(path, info); err != nil {
		return nil, err
	}
	content, err := readBounded(f, path)
	if err != nil {
		return nil, err
	}
	key, err := ParseKey(content)
	if err != nil {
		return nil, fmt.Errorf("faucet key file %s: %w", path, err)
	}
	return key, nil
}

func requireOwnedByUs(path string, info os.FileInfo) error {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("faucet key file %s: this platform does not report its owner", path)
	}
	if uid := os.Geteuid(); int(st.Uid) != uid {
		return fmt.Errorf("faucet key file %s is owned by uid %d and the gateway runs as uid %d: chown %s %s", path, st.Uid, uid, GatewayUser, path)
	}
	return nil
}

// readBounded reads at most maxKeyFileBytes of f; a longer file is not a key.
func readBounded(f *os.File, path string) ([]byte, error) {
	content, err := io.ReadAll(io.LimitReader(f, maxKeyFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read faucet key file %s: %w", path, err)
	}
	if len(content) > maxKeyFileBytes {
		return nil, fmt.Errorf("faucet key file %s is over %d bytes: it is not a faucet key", path, maxKeyFileBytes)
	}
	return content, nil
}
