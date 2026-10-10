package main

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
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
