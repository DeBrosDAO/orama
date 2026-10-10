package tx_test

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/log/v2"

	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/client"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/crypto/hd"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"
	signing "github.com/cosmos/cosmos-sdk/types/tx/signing"
	authsigning "github.com/cosmos/cosmos-sdk/x/auth/signing"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"

	"github.com/DeBrosOfficial/network/chain/app"
	"github.com/DeBrosOfficial/network/chain/app/params"
	oramatx "github.com/DeBrosOfficial/network/chain/client/tx"
)

// abandonMnemonic is the BIP-39 test mnemonic. Its Cosmos leaf at
// params.CoinType, account 0, index 0 is the RootWallet ORAMA vector
// (core/pkg/rwagent/orama_tx_test.go). The path is read from the SDK config
// after the chain coin type is applied, not written out here.
const abandonMnemonic = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"

const (
	vectorAddress = "orama19rl4cm2hmr8afy4kldpxz3fka4jguq0a5tup0s"
	vectorPubKey  = "024f4e2ad99c34d60b9ba6283c9431a8418af8673212961f97a77b6377fcd05b62"
)

var (
	txConfigOnce sync.Once
	sharedCfg    client.TxConfig
	sharedReg    codectypes.InterfaceRegistry
)

// oramaTxConfig is the chain app's encoder, the same TxConfig oramad builds.
// Constructing it pulls chain/app into this test. There is no second codec.
func oramaTxConfig(t *testing.T) client.TxConfig {
	t.Helper()
	txConfigOnce.Do(func() {
		app.SetAddressPrefixes()
		oramaApp := app.NewOramaApp(log.NewNopLogger(), dbm.NewMemDB(), false, simtestutil.EmptyAppOptions{})
		sharedCfg = oramaApp.TxConfig()
		sharedReg = oramaApp.InterfaceRegistry()
	})
	require.NotNil(t, sharedCfg)
	return sharedCfg
}

// oramaInterfaceRegistry is the chain app's registry: every registered Msg.
func oramaInterfaceRegistry(t *testing.T) codectypes.InterfaceRegistry {
	t.Helper()
	oramaTxConfig(t)
	require.NotNil(t, sharedReg)
	return sharedReg
}

func newBuilder(t *testing.T) *oramatx.Builder {
	t.Helper()
	b, err := oramatx.New(oramaTxConfig(t))
	require.NoError(t, err)
	return b
}

func mustLeaf(t *testing.T, mnemonic string) []byte {
	t.Helper()
	app.SetAddressPrefixes()
	path := sdk.GetConfig().GetFullBIP44Path()
	require.Contains(t, path, fmt.Sprintf("/%d'/", params.CoinType), "path must use params.CoinType")
	bz, err := hd.Secp256k1.Derive()(mnemonic, "", path)
	require.NoError(t, err)
	require.Len(t, bz, secp256k1.PrivKeySize)
	return bz
}

func mustAccount(t *testing.T) oramatx.Account {
	t.Helper()
	acc, err := oramatx.DeriveAccount(secp256k1.GenPrivKey().Bytes())
	require.NoError(t, err)
	return acc
}

func mustAccAddress(t *testing.T, bech32 string) sdk.AccAddress {
	t.Helper()
	addr, err := sdk.AccAddressFromBech32(bech32)
	require.NoError(t, err)
	return addr
}

func TestDeriveAccount_oramaAddressFromSecp256k1Key(t *testing.T) {
	leaf := mustLeaf(t, abandonMnemonic)
	acc, err := oramatx.DeriveAccount(leaf)
	require.NoError(t, err)

	// The SDK derives the leaf. DeriveAccount must format that key, not derive
	// it a second time, or this address would not be the vector's.
	priv := hd.Secp256k1.Generate()(leaf)
	require.Equal(t, sdk.AccAddress(priv.PubKey().Address()).String(), acc.Address)
	require.Equal(t, vectorAddress, acc.Address)
	require.Equal(t, vectorPubKey, hex.EncodeToString(priv.PubKey().Bytes()))
	require.True(t, strings.HasPrefix(acc.Address, params.Bech32Prefix+"1"))
	require.Equal(t, uint32(params.CoinType), sdk.GetConfig().GetCoinType())
	require.Contains(t, sdk.GetConfig().GetFullBIP44Path(), fmt.Sprintf("/%d'/", params.CoinType))

	codecAddr, err := oramaTxConfig(t).SigningContext().AddressCodec().BytesToString(priv.PubKey().Address())
	require.NoError(t, err)
	require.Equal(t, codecAddr, acc.Address)

	_, err = oramatx.DeriveAccount(leaf[:len(leaf)-1])
	require.Error(t, err)
	_, err = oramatx.DeriveAccount(make([]byte, secp256k1.PrivKeySize))
	require.Error(t, err)
}

func TestDeriveAccount_appliesParamsCoinTypeAndPrefixes(t *testing.T) {
	cfg := sdk.GetConfig()
	// Clobber the SDK config so this test fails unless DeriveAccount writes
	// params.CoinType and the orama prefixes itself, rather than trusting a
	// previous SetAddressPrefixes call.
	cfg.SetBech32PrefixForAccount(sdk.Bech32PrefixAccAddr, sdk.Bech32PrefixAccPub)
	cfg.SetBech32PrefixForValidator(sdk.Bech32PrefixValAddr, sdk.Bech32PrefixValPub)
	cfg.SetBech32PrefixForConsensusNode(sdk.Bech32PrefixConsAddr, sdk.Bech32PrefixConsPub)
	cfg.SetCoinType(params.CoinType + 1)

	acc, err := oramatx.DeriveAccount(secp256k1.GenPrivKey().Bytes())
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(acc.Address, params.Bech32Prefix+"1"))
	require.Equal(t, uint32(params.CoinType), cfg.GetCoinType())
	require.Equal(t, params.Bech32Prefix, cfg.GetBech32AccountAddrPrefix())
	require.Equal(t, params.Bech32PrefixAccPub, cfg.GetBech32AccountPubPrefix())
	require.Equal(t, params.Bech32PrefixValAddr, cfg.GetBech32ValidatorAddrPrefix())
	require.Equal(t, params.Bech32PrefixValPub, cfg.GetBech32ValidatorPubPrefix())
	require.Equal(t, params.Bech32PrefixConsAddr, cfg.GetBech32ConsensusAddrPrefix())
	require.Equal(t, params.Bech32PrefixConsPub, cfg.GetBech32ConsensusPubPrefix())
}

// TestBuild_encodesUserNoramaMsgSend encodes a norama MsgSend from one user
// account to another and reads it back. It does not broadcast the transaction;
// it only checks the encoding.
func TestBuild_encodesUserNoramaMsgSend(t *testing.T) {
	b := newBuilder(t)
	from := mustAccount(t)
	to := mustAccount(t)
	require.NotEqual(t, from.Address, to.Address)

	const (
		chainID = "orama-localnet-txbuilder-1"
		accNum  = 7
		seq     = 3
		gas     = 200_000
	)
	amount := sdk.NewCoins(sdk.NewInt64Coin(params.BaseDenom, 1_500_000_000))
	fee := sdk.NewCoins(sdk.NewInt64Coin(params.BaseDenom, 5_000))
	msg := banktypes.NewMsgSend(mustAccAddress(t, from.Address), mustAccAddress(t, to.Address), amount)

	raw, err := b.Build(from, oramatx.Unsigned{
		ChainID:       chainID,
		AccountNumber: accNum,
		Sequence:      seq,
		GasLimit:      gas,
		Fee:           fee,
		Msgs:          []sdk.Msg{msg},
		Memo:          "encode only",
	})
	require.NoError(t, err)
	require.NotEmpty(t, raw)

	again, err := b.Build(from, oramatx.Unsigned{
		ChainID:       chainID,
		AccountNumber: accNum,
		Sequence:      seq,
		GasLimit:      gas,
		Fee:           fee,
		Msgs:          []sdk.Msg{msg},
		Memo:          "encode only",
	})
	require.NoError(t, err)
	require.Equal(t, raw, again, "SIGN_MODE_DIRECT signing is deterministic")

	decoded, err := b.Decode(raw, chainID, accNum)
	require.NoError(t, err)
	require.Equal(t, from.Address, decoded.Signer)
	require.NotEqual(t, to.Address, decoded.Signer)
	require.True(t, decoded.Fee.Equal(fee))
	require.Equal(t, []string{"/cosmos.bank.v1beta1.MsgSend"}, decoded.MsgTypeURLs)
	for _, coin := range decoded.Fee {
		require.Equal(t, params.BaseDenom, coin.Denom)
	}
}

func TestBuild_refusesEmptyChainID(t *testing.T) {
	b := newBuilder(t)
	from := mustAccount(t)
	to := mustAccount(t)
	msg := banktypes.NewMsgSend(
		mustAccAddress(t, from.Address),
		mustAccAddress(t, to.Address),
		sdk.NewCoins(sdk.NewInt64Coin(params.BaseDenom, 1)),
	)
	raw, err := b.Build(from, oramatx.Unsigned{
		ChainID:  "",
		Fee:      sdk.NewCoins(sdk.NewInt64Coin(params.BaseDenom, 1)),
		Msgs:     []sdk.Msg{msg},
		GasLimit: 1,
	})
	require.ErrorIs(t, err, oramatx.ErrEmptyChainID)
	require.Nil(t, raw)
}

func TestBuild_refusesFeeDenom(t *testing.T) {
	b := newBuilder(t)
	from := mustAccount(t)
	to := mustAccount(t)
	msg := banktypes.NewMsgSend(
		mustAccAddress(t, from.Address),
		mustAccAddress(t, to.Address),
		sdk.NewCoins(sdk.NewInt64Coin(params.BaseDenom, 1)),
	)
	base := oramatx.Unsigned{
		ChainID:  "orama-localnet-txbuilder-1",
		Msgs:     []sdk.Msg{msg},
		GasLimit: 1,
	}
	for _, fee := range []sdk.Coins{
		nil,
		sdk.NewCoins(sdk.NewInt64Coin("uatom", 1)),
		sdk.NewCoins(sdk.NewInt64Coin(params.BaseDenom, 1), sdk.NewInt64Coin("uatom", 1)),
	} {
		tx := base
		tx.Fee = fee
		raw, err := b.Build(from, tx)
		require.ErrorIs(t, err, oramatx.ErrFeeDenom)
		require.ErrorContains(t, err, params.BaseDenom)
		require.Nil(t, raw)
	}
}

func TestBuild_refusesAccountThatIsNotSigner(t *testing.T) {
	b := newBuilder(t)
	signer := mustAccount(t)
	other := mustAccount(t)
	to := mustAccount(t)
	msg := banktypes.NewMsgSend(
		mustAccAddress(t, other.Address),
		mustAccAddress(t, to.Address),
		sdk.NewCoins(sdk.NewInt64Coin(params.BaseDenom, 1)),
	)
	raw, err := b.Build(signer, oramatx.Unsigned{
		ChainID:  "orama-localnet-txbuilder-1",
		Fee:      sdk.NewCoins(sdk.NewInt64Coin(params.BaseDenom, 1)),
		Msgs:     []sdk.Msg{msg},
		GasLimit: 1,
	})
	require.ErrorIs(t, err, oramatx.ErrNotSigner)
	require.Nil(t, raw)
}

func TestDecode_refusesEmptyChainID(t *testing.T) {
	b := newBuilder(t)
	raw := mustEncodedSend(t, b)
	_, err := b.Decode(raw, "", 7)
	require.ErrorIs(t, err, oramatx.ErrEmptyChainID)
}

func TestDecode_refusesFeeDenom(t *testing.T) {
	cfg := oramaTxConfig(t)
	b := newBuilder(t)
	priv := secp256k1.GenPrivKey()
	from, err := oramatx.DeriveAccount(priv.Bytes())
	require.NoError(t, err)
	to := mustAccount(t)
	msg := banktypes.NewMsgSend(
		mustAccAddress(t, from.Address),
		mustAccAddress(t, to.Address),
		sdk.NewCoins(sdk.NewInt64Coin(params.BaseDenom, 1)),
	)
	raw := signWithKey(t, cfg, msg, priv, "orama-localnet-txbuilder-1", 1, 0, 1000, sdk.NewCoins(sdk.NewInt64Coin("uatom", 5)))
	_, err = b.Decode(raw, "orama-localnet-txbuilder-1", 1)
	require.ErrorIs(t, err, oramatx.ErrFeeDenom)
}

func TestDecode_refusesSignatureThatDoesNotMatchSigner(t *testing.T) {
	cfg := oramaTxConfig(t)
	b := newBuilder(t)
	from := mustAccount(t)
	to := mustAccount(t)
	const (
		chainID = "orama-localnet-txbuilder-1"
		accNum  = 7
		seq     = 3
		gas     = 200_000
	)
	fee := sdk.NewCoins(sdk.NewInt64Coin(params.BaseDenom, 5_000))
	msg := banktypes.NewMsgSend(
		mustAccAddress(t, from.Address),
		mustAccAddress(t, to.Address),
		sdk.NewCoins(sdk.NewInt64Coin(params.BaseDenom, 1)),
	)
	raw, err := b.Build(from, oramatx.Unsigned{
		ChainID: chainID, AccountNumber: accNum, Sequence: seq, GasLimit: gas, Fee: fee, Msgs: []sdk.Msg{msg},
	})
	require.NoError(t, err)

	t.Run("wrong chain id", func(t *testing.T) {
		_, err := b.Decode(raw, chainID+"-other", accNum)
		require.ErrorIs(t, err, oramatx.ErrSignature)
	})
	t.Run("wrong account number", func(t *testing.T) {
		_, err := b.Decode(raw, chainID, accNum+1)
		require.ErrorIs(t, err, oramatx.ErrSignature)
	})
	t.Run("pubkey is not the message signer", func(t *testing.T) {
		foreign := secp256k1.GenPrivKey()
		signed := signWithKey(t, cfg, msg, foreign, chainID, accNum, seq, gas, fee)
		_, err := b.Decode(signed, chainID, accNum)
		require.ErrorIs(t, err, oramatx.ErrSignature)
	})
}

func mustEncodedSend(t *testing.T, b *oramatx.Builder) []byte {
	t.Helper()
	from := mustAccount(t)
	to := mustAccount(t)
	msg := banktypes.NewMsgSend(
		mustAccAddress(t, from.Address),
		mustAccAddress(t, to.Address),
		sdk.NewCoins(sdk.NewInt64Coin(params.BaseDenom, 1)),
	)
	raw, err := b.Build(from, oramatx.Unsigned{
		ChainID:       "orama-localnet-txbuilder-1",
		AccountNumber: 7,
		Sequence:      1,
		GasLimit:      1000,
		Fee:           sdk.NewCoins(sdk.NewInt64Coin(params.BaseDenom, 1)),
		Msgs:          []sdk.Msg{msg},
	})
	require.NoError(t, err)
	return raw
}

// signWithKey builds a SIGN_MODE_DIRECT tx signed by priv, which may not be
// the message signer. It uses the app encoder directly so the builder's own
// signer check can be tested from the outside.
func signWithKey(t *testing.T, cfg client.TxConfig, msg sdk.Msg, priv *secp256k1.PrivKey, chainID string, accNum, seq, gas uint64, fee sdk.Coins) []byte {
	t.Helper()
	bld := cfg.NewTxBuilder()
	require.NoError(t, bld.SetMsgs(msg))
	bld.SetFeeAmount(fee)
	bld.SetGasLimit(gas)
	mode := signing.SignMode_SIGN_MODE_DIRECT
	sig := signing.SignatureV2{
		PubKey:   priv.PubKey(),
		Data:     &signing.SingleSignatureData{SignMode: mode},
		Sequence: seq,
	}
	require.NoError(t, bld.SetSignatures(sig))
	signerData := authsigning.SignerData{
		Address:       sdk.AccAddress(priv.PubKey().Address()).String(),
		ChainID:       chainID,
		AccountNumber: accNum,
		Sequence:      seq,
		PubKey:        priv.PubKey(),
	}
	signBytes, err := authsigning.GetSignBytesAdapter(context.Background(), cfg.SignModeHandler(), mode, signerData, bld.GetTx())
	require.NoError(t, err)
	sigBytes, err := priv.Sign(signBytes)
	require.NoError(t, err)
	sig.Data = &signing.SingleSignatureData{SignMode: mode, Signature: sigBytes}
	require.NoError(t, bld.SetSignatures(sig))
	bz, err := cfg.TxEncoder()(bld.GetTx())
	require.NoError(t, err)
	return bz
}

// agentSigner signs through a function, the way a signing agent does: the builder never sees a key.
type agentSigner struct {
	acc  oramatx.Account
	sign func([]byte) ([]byte, error)
}

func (a agentSigner) AccountAddress() string { return a.acc.AccountAddress() }

func (a agentSigner) PublicKey() cryptotypes.PubKey { return a.acc.PublicKey() }

func (a agentSigner) Sign(b []byte) ([]byte, error) { return a.sign(b) }

func TestBuildWith_agentSignerProducesTheSameTxAsAnAccount(t *testing.T) {
	b := newBuilder(t)
	from, to := mustAccount(t), mustAccount(t)
	unsigned := oramatx.Unsigned{
		ChainID: "orama-localnet-txbuilder-1", AccountNumber: 1, Sequence: 2, GasLimit: 100_000,
		Fee: sdk.NewCoins(sdk.NewInt64Coin(params.BaseDenom, 100)),
		Msgs: []sdk.Msg{banktypes.NewMsgSend(mustAccAddress(t, from.Address), mustAccAddress(t, to.Address),
			sdk.NewCoins(sdk.NewInt64Coin(params.BaseDenom, 5)))},
	}
	want, err := b.Build(from, unsigned)
	require.NoError(t, err)

	var signed [][]byte
	got, err := b.BuildWith(agentSigner{acc: from, sign: func(msg []byte) ([]byte, error) {
		signed = append(signed, msg)
		return from.Sign(msg)
	}}, unsigned)
	require.NoError(t, err)
	require.Equal(t, want, got)
	require.Len(t, signed, 1, "the agent is asked once, for the sign bytes")
}

func TestBuildWith_refusesASignerThatIsNotTheMessageSigner(t *testing.T) {
	b := newBuilder(t)
	from, other := mustAccount(t), mustAccount(t)
	_, err := b.BuildWith(other, oramatx.Unsigned{
		ChainID: "orama-localnet-txbuilder-1", GasLimit: 1,
		Fee: sdk.NewCoins(sdk.NewInt64Coin(params.BaseDenom, 1)),
		Msgs: []sdk.Msg{banktypes.NewMsgSend(mustAccAddress(t, from.Address), mustAccAddress(t, other.Address),
			sdk.NewCoins(sdk.NewInt64Coin(params.BaseDenom, 1)))},
	})
	require.ErrorIs(t, err, oramatx.ErrNotSigner)
}

func TestBuildWith_refusesASignerWithoutAPublicKey(t *testing.T) {
	_, err := newBuilder(t).BuildWith(oramatx.Account{}, oramatx.Unsigned{ChainID: "c"})
	require.Error(t, err)
}
