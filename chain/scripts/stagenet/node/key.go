package main

import (
	"bufio"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
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
