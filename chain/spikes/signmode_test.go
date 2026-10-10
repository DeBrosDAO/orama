package spikes_test

import (
	"crypto/sha256"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cosmos/cosmos-sdk/crypto/hd"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/tx"
	signingtypes "github.com/cosmos/cosmos-sdk/types/tx/signing"
	authtx "github.com/cosmos/cosmos-sdk/x/auth/tx"

	"github.com/DeBrosOfficial/network/chain/app/params"
)

// Test mnemonic from BIP-39. Not a key anyone should fund.
const spikeMnemonic = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"

func deriveSecp256k1(t *testing.T, coinType, account, index uint32) *secp256k1.PrivKey {
	t.Helper()
	path := hd.CreateHDPath(coinType, account, index)
	raw, err := hd.Secp256k1.Derive()(spikeMnemonic, "", path.String())
	require.NoError(t, err)
	key, ok := hd.Secp256k1.Generate()(raw).(*secp256k1.PrivKey)
	require.True(t, ok)
	return key
}

func TestSignModeDirectAccountFormat(t *testing.T) {
	// D8: secp256k1, SIGN_MODE_DIRECT, a separate account from the Ethereum key.
	// Coin type 60 is Ethereum's SLIP-44. Coin type 118 is Cosmos. Account 1
	// is the dedicated Orama account under 118, not account 0.
	ethKey := deriveSecp256k1(t, 60, 0, 0)
	cosmos0 := deriveSecp256k1(t, params.CoinType, 0, 0)
	oramaKey := deriveSecp256k1(t, params.CoinType, 1, 0)

	require.NotEqual(t, ethKey.PubKey().Bytes(), cosmos0.PubKey().Bytes(), "coin type 118 account 0 is already not the Ethereum key")
	require.NotEqual(t, cosmos0.PubKey().Bytes(), oramaKey.PubKey().Bytes(), "account index 1 is a different key from account 0")
	require.Len(t, oramaKey.PubKey().Bytes(), 33)
	require.Len(t, oramaKey.PubKey().Address(), 20)

	addr := sdk.MustBech32ifyAddressBytes(params.Bech32Prefix, oramaKey.PubKey().Address())
	require.Contains(t, addr, "orama1")
	val := sdk.MustBech32ifyAddressBytes(params.Bech32PrefixValAddr, oramaKey.PubKey().Address())
	require.Contains(t, val, "oramavaloper1")

	body := []byte("spike-body")
	authInfo := []byte("spike-auth-info")
	signDocBytes, err := authtx.DirectSignBytes(body, authInfo, "orama-spike-1", 7)
	require.NoError(t, err)

	var parsed tx.SignDoc
	require.NoError(t, parsed.Unmarshal(signDocBytes))
	require.Equal(t, body, parsed.BodyBytes)
	require.Equal(t, authInfo, parsed.AuthInfoBytes)
	require.Equal(t, "orama-spike-1", parsed.ChainId)
	require.Equal(t, uint64(7), parsed.AccountNumber)
	require.NotContains(t, string(signDocBytes), "EIP712")

	sig, err := oramaKey.Sign(signDocBytes)
	require.NoError(t, err)
	require.Len(t, sig, 64)
	pub := oramaKey.PubKey()
	require.True(t, pub.VerifySignature(signDocBytes, sig), "the chain hashes the SignDoc once inside VerifySignature")

	// A generic wallet path that signs the already-hashed digest (EIP-191,
	// personal_sign, or a second SHA-256) does not verify.
	digest := sha256.Sum256(signDocBytes)
	doubleHashed, err := oramaKey.Sign(digest[:])
	require.NoError(t, err)
	require.False(t, pub.VerifySignature(signDocBytes, doubleHashed))

	// SDK v0.54.4 has no EIP-712 sign mode. EIP-191 is a registered enum
	// value with no default handler.
	require.Equal(t, signingtypes.SignMode(191), signingtypes.SignMode_SIGN_MODE_EIP_191)
	_, isEIP712 := signingtypes.SignMode_value["SIGN_MODE_EIP712"]
	require.False(t, isEIP712)
	for _, mode := range authtx.DefaultSignModes {
		require.NotEqual(t, signingtypes.SignMode_SIGN_MODE_EIP_191, mode)
	}
	require.Equal(t, signingtypes.SignMode_SIGN_MODE_DIRECT, authtx.DefaultSignModes[0])

	t.Logf("path=%s address=%s pubkey=%x sig_ok=true eip712_enum=absent",
		hd.CreateHDPath(params.CoinType, 1, 0).String(), addr, pub.Bytes())
	t.Logf("coin60_pubkey=%x coin118_account0_pubkey=%x", ethKey.PubKey().Bytes(), cosmos0.PubKey().Bytes())
}
