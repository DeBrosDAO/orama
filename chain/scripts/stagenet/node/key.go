package main

import (
	"bufio"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/DeBrosOfficial/network/chain/client/tx"
)

// keyHex is a 32-byte secp256k1 private key in hex, the one line `oramad keys export
// --unarmored-hex` prints.
var keyHex = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

// readKey returns the first line of r that is a 64-digit hex string. Other lines (a warning
// cobra printed to the same stream) are skipped; no line at all is an error that never echoes
// the input, since the input is key material.
func readKey(r io.Reader) ([]byte, error) {
	sc := bufio.NewScanner(io.LimitReader(r, 1<<16))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if keyHex.MatchString(line) {
			return hex.DecodeString(line)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read the key: %w", err)
	}
	return nil, errors.New("no 64-digit hex private key on stdin (pipe `oramad keys export validator --unarmored-hex --unsafe -y`)")
}

// accountFromStdin derives the operator account from the key on r.
func accountFromStdin(r io.Reader) (tx.Account, error) {
	key, err := readKey(r)
	if err != nil {
		return tx.Account{}, err
	}
	return tx.DeriveAccount(key)
}

// addressOfKeyFile is the orama address of a hex key file such as a provider's hot-key.
func addressOfKeyFile(path string) (string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	key, err := hex.DecodeString(strings.TrimSpace(string(body)))
	if err != nil {
		return "", fmt.Errorf("%s is not hex", path)
	}
	acct, err := tx.DeriveAccount(key)
	if err != nil {
		return "", fmt.Errorf("%s: %w", path, err)
	}
	return acct.Address, nil
}
