package main

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	"github.com/cosmos/cosmos-sdk/types/bech32"

	"github.com/DeBrosOfficial/network/chain/client/tx"
	shieldedtypes "github.com/DeBrosOfficial/network/chain/x/shielded/types"
)

const (
	committedScenario = "../../../x/shielded/wallet/testdata/bundles/scenario.json"
	scenarioChain     = "orama-localnet-shielded-wallet-1"
	// aliceHex is the address the committed scenario's unshield is signed for (scenario.rs,
	// UNSHIELD_SIGNER).
	aliceHex = "5f64e1128afde934c792939c8e64fca46b7f71f6"
)

func committed(t *testing.T) scenario {
	t.Helper()
	data, err := os.ReadFile(filepath.FromSlash(committedScenario))
	require.NoError(t, err)
	sc, err := loadScenario(data, scenarioChain)
	require.NoError(t, err)
	return sc
}

func aliceAddress(t *testing.T) string {
	t.Helper()
	raw, err := hex.DecodeString(aliceHex)
	require.NoError(t, err)
	addr, err := bech32.ConvertAndEncode("orama", raw)
	require.NoError(t, err)
	return addr
}

func TestLoadScenario_refusesAnotherChainAndAWrongShape(t *testing.T) {
	data, err := os.ReadFile(filepath.FromSlash(committedScenario))
	require.NoError(t, err)
	_, err = loadScenario(data, "orama-stagenet-1")
	require.ErrorContains(t, err, "not \"orama-stagenet-1\"")
	_, err = loadScenario([]byte(`{"chain_id":"c","steps":[]}`), "c")
	require.ErrorContains(t, err, "0 steps")
	_, err = loadScenario([]byte(`{"chain_id":"c","steps":[{"kind":"shield"},{"kind":"transfer"},{"kind":"transfer"},{"kind":"unshield"}]}`), "c")
	require.ErrorContains(t, err, "step 1")
	_, err = loadScenario([]byte("nope"), "c")
	require.Error(t, err)
}

func TestStepMessage_mapsEachKindToItsMessage(t *testing.T) {
	sc := committed(t)
	signer := aliceAddress(t)
	const actionGas = 10

	m, gas, signerless, err := stepMessage(sc.Steps[0], signer, actionGas)
	require.NoError(t, err)
	require.IsType(t, &shieldedtypes.MsgShieldEarnings{}, m, "a stagenet account has earnings, not a bank balance")
	require.False(t, signerless)
	require.Zero(t, gas)

	m, gas, signerless, err = stepMessage(sc.Steps[2], signer, actionGas)
	require.NoError(t, err)
	tr, ok := m.(*shieldedtypes.MsgShieldedTransfer)
	require.True(t, ok)
	require.Equal(t, shieldedtypes.SignerlessAddress().String(), tr.Signer)
	require.True(t, signerless)
	require.Equal(t, uint64(len(sc.Steps[2].Cmxs))*actionGas, gas, "exactly the actions' gas")

	m, _, signerless, err = stepMessage(sc.Steps[3], signer, actionGas)
	require.NoError(t, err, "the chain's binding for this signer is the wallet's")
	un, ok := m.(*shieldedtypes.MsgUnshield)
	require.True(t, ok)
	require.Equal(t, shieldedtypes.UnshieldTargetFeeTopup, un.Target)
	require.False(t, signerless)
}

func TestStepMessage_refusesAnUnshieldBoundToAnotherSigner(t *testing.T) {
	sc := committed(t)
	acct, err := tx.DeriveAccount(secp256k1.GenPrivKey().Bytes())
	require.NoError(t, err)
	_, _, _, err = stepMessage(sc.Steps[3], acct.Address, 10)
	require.ErrorContains(t, err, "the chain computes")
}

func TestStepMessage_refusesABadBundleAndAnUnknownKind(t *testing.T) {
	_, _, _, err := stepMessage(scenarioStep{Name: "s", Kind: "shield", Bundle: "zz"}, "orama1x", 1)
	require.Error(t, err)
	_, _, _, err = stepMessage(scenarioStep{Name: "s", Kind: "mint", Bundle: "00"}, "orama1x", 1)
	require.ErrorContains(t, err, "unknown kind")
}

func TestAnchorMatches(t *testing.T) {
	step := scenarioStep{Anchor: "abcd"}
	require.NoError(t, anchorMatches(step, nil, 0, true), "an empty pool is the first step's anchor")
	require.ErrorContains(t, anchorMatches(step, []byte{0xab, 0xcd}, 5, true), "needs an empty pool")
	require.NoError(t, anchorMatches(step, []byte{0xab, 0xcd}, 2, false))
	require.ErrorContains(t, anchorMatches(step, []byte{0x00}, 2, false), "the chain's tree root")
}

func TestSuggestedTransferFee(t *testing.T) {
	// Two actions of 50,000 gas at a base fee of 1 norama, margin 2, plus two nullifier fees of 1000.
	got := suggestedTransferFee(2, 50_000, math.NewInt(1), math.NewInt(1000))
	require.Equal(t, "202001", got.String())
	// It always covers what the chain takes at the current base fee: 2*50000*1 + 2*1000 = 102000.
	require.True(t, got.GT(math.NewInt(102_000)))
}

func TestAddressBytesHex(t *testing.T) {
	addr := aliceAddress(t)
	got, err := addressBytesHex(addr)
	require.NoError(t, err)
	require.Equal(t, aliceHex, got)

	_, err = addressBytesHex("nope")
	require.Error(t, err)
	other, err := bech32.ConvertAndEncode("cosmos", make([]byte, 20))
	require.NoError(t, err)
	_, err = addressBytesHex(other)
	require.ErrorContains(t, err, "not an orama account")
}
