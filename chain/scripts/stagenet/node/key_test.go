package main

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"

	"github.com/DeBrosOfficial/network/chain/client/tx"
)

func TestReadKey_takesTheHexLineAndSkipsWarnings(t *testing.T) {
	priv := secp256k1.GenPrivKey().Bytes()
	in := "WARNING: The private key will be exported\n" + hex.EncodeToString(priv) + "\n"
	got, err := readKey(strings.NewReader(in))
	require.NoError(t, err)
	require.Equal(t, priv, got)
}

func TestReadKey_refusesInputWithoutAKeyAndNeverEchoesIt(t *testing.T) {
	for _, in := range []string{"", "\n\n", "not a key\n", strings.Repeat("a", 63) + "\n", strings.Repeat("g", 64) + "\n"} {
		_, err := readKey(strings.NewReader(in))
		require.Error(t, err, in)
		if strings.TrimSpace(in) != "" {
			require.NotContains(t, err.Error(), strings.TrimSpace(in))
		}
	}
}

func TestAddressOfKeyFile_matchesTheAccountAndRefusesBadFiles(t *testing.T) {
	priv := secp256k1.GenPrivKey().Bytes()
	acct, err := tx.DeriveAccount(priv)
	require.NoError(t, err)
	dir := t.TempDir()
	path := filepath.Join(dir, "hot-key")
	require.NoError(t, os.WriteFile(path, []byte(hex.EncodeToString(priv)+"\n"), 0o600))
	addr, err := addressOfKeyFile(path)
	require.NoError(t, err)
	require.Equal(t, acct.Address, addr)

	_, err = addressOfKeyFile(filepath.Join(dir, "missing"))
	require.Error(t, err)
	bad := filepath.Join(dir, "bad")
	require.NoError(t, os.WriteFile(bad, []byte("zz"), 0o600))
	_, err = addressOfKeyFile(bad)
	require.ErrorContains(t, err, "not hex")
	short := filepath.Join(dir, "short")
	require.NoError(t, os.WriteFile(short, []byte("abcd"), 0o600))
	_, err = addressOfKeyFile(short)
	require.Error(t, err)
}
