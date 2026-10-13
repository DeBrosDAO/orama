package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/DeBrosOfficial/network/chain/client/tx"
)

// hotKeyLen is a secp256k1 private key.
const hotKeyLen = 32

// loadOrCreateHotKey reads the service's hex secp256k1 key from path, or
// writes a new one with mode 0600 when the file does not exist. A key file
// any other user can read is refused: the hot key pays this node's fees.
func loadOrCreateHotKey(path string) (tx.Account, bool, error) {
	acct, err := readHotKey(path)
	if errors.Is(err, os.ErrNotExist) {
		acct, err := createHotKey(path)
		return acct, true, err
	}
	return acct, false, err
}

// readHotKey reads the service's hex secp256k1 key from path and refuses a
// file any other user can read. An absent file is os.ErrNotExist.
func readHotKey(path string) (tx.Account, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return tx.Account{}, fmt.Errorf("read hot key %s: %w", path, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return tx.Account{}, fmt.Errorf("stat hot key %s: %w", path, err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return tx.Account{}, fmt.Errorf("hot key %s is mode %o; it must be 0600", path, info.Mode().Perm())
	}
	key, err := hex.DecodeString(strings.TrimSpace(string(body)))
	if err != nil || len(key) != hotKeyLen {
		return tx.Account{}, fmt.Errorf("hot key %s is not %d hex bytes", path, hotKeyLen)
	}
	acct, err := tx.DeriveAccount(key)
	if err != nil {
		return tx.Account{}, fmt.Errorf("hot key %s: %w", path, err)
	}
	return acct, nil
}

func createHotKey(path string) (tx.Account, error) {
	for {
		key := make([]byte, hotKeyLen)
		if _, err := rand.Read(key); err != nil {
			return tx.Account{}, fmt.Errorf("generate hot key: %w", err)
		}
		acct, err := tx.DeriveAccount(key)
		if err != nil {
			// A random 32-byte value outside [1, N-1] is not a scalar; draw again.
			continue
		}
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return tx.Account{}, fmt.Errorf("create hot key %s: %w", path, err)
		}
		if _, err := f.WriteString(hex.EncodeToString(key) + "\n"); err != nil {
			f.Close()
			_ = os.Remove(path)
			return tx.Account{}, fmt.Errorf("write hot key %s: %w", path, err)
		}
		if err := f.Sync(); err != nil {
			f.Close()
			_ = os.Remove(path)
			return tx.Account{}, fmt.Errorf("sync hot key %s: %w", path, err)
		}
		if err := f.Close(); err != nil {
			return tx.Account{}, fmt.Errorf("close hot key %s: %w", path, err)
		}
		return acct, nil
	}
}

// readNodeID reads the x/nodes id this node registered under.
func readNodeID(path string) (string, error) {
	return readOneLine(path, "the x/nodes id; register the node and write its id there")
}

// readOneLine reads a file that holds exactly one token. what says what the
// file must contain, for the error when it is missing.
func readOneLine(path, what string) (string, error) {
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("%s does not exist; it must hold %s", path, what)
	}
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	v := strings.TrimSpace(string(body))
	if v == "" || strings.ContainsAny(v, " \t\n") {
		return "", fmt.Errorf("%s does not hold one value", path)
	}
	return v, nil
}
