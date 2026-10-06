//go:build cgo && orchardffi

package app_test

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	emissiontypes "github.com/DeBrosOfficial/network/chain/x/emission/types"
	"github.com/DeBrosOfficial/network/chain/x/shielded/keeper"
	shieldedtypes "github.com/DeBrosOfficial/network/chain/x/shielded/types"
)

const walletScenarioPath = "../x/shielded/wallet/testdata/bundles/scenario.json"

type walletStep struct {
	Name         string   `json:"name"`
	Kind         string   `json:"kind"`
	Bundle       string   `json:"bundle"`
	Anchor       string   `json:"anchor"`
	ValueBalance int64    `json:"value_balance"`
	Binding      string   `json:"binding"`
	Nullifiers   []string `json:"nullifiers"`
	Cmxs         []string `json:"cmxs"`
}

func loadWalletScenario(t *testing.T) (chainID string, steps []walletStep) {
	t.Helper()
	raw, err := os.ReadFile(walletScenarioPath)
	require.NoError(t, err)
	var sc struct {
		ChainID string       `json:"chain_id"`
		Steps   []walletStep `json:"steps"`
	}
	require.NoError(t, json.Unmarshal(raw, &sc))
	require.Len(t, sc.Steps, 4)
	return sc.ChainID, sc.Steps
}

func mustDecode(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	require.NoError(t, err)
	return b
}

// grantEarnings gives an account earnings the way the other app tests do.
func (c *shieldedChain) grantEarnings(t *testing.T, who sdk.AccAddress, amount int64) {
	t.Helper()
	ctx := c.app.NewNextBlockContext(cmtproto.Header{Height: c.height + 1, Time: c.blockTime(c.height + 1)})
	coin := sdk.NewInt64Coin(params.BaseDenom, amount)
	require.NoError(t, c.app.BankKeeper.MintCoins(ctx, emissiontypes.ModuleName, sdk.NewCoins(coin)))
	require.NoError(t, c.app.FeesKeeper.CreditEarnings(ctx, emissiontypes.ModuleName, who, coin))
	writeCache(t, ctx)
	_, err := c.app.Commit()
	require.NoError(t, err)
	c.height++
}

func (c *shieldedChain) treeRoot(t *testing.T) []byte {
	t.Helper()
	ctx := c.app.NewContextLegacy(true, cmtprotoHeader(c.height, c.blockTime(c.height)))
	resp, err := keeper.NewQueryServerImpl(c.app.ShieldedKeeper).TreeState(ctx, &shieldedtypes.QueryTreeStateRequest{})
	require.NoError(t, err)
	return resp.CurrentRoot
}

// The F7 wallet builder's scenario (real proofs, real spends) run through the chain in order: each
// bundle's anchor is the tree the chain has built from the bundles before it, the transfer pays a
// real fee, the unshield is signed for its signer and target, and every message is accepted by both
// real verifiers through FinalizeBlock.
func TestShieldedReal_theWalletBuilderScenarioRunsThroughTheChain(t *testing.T) {
	chainID, steps := loadWalletScenario(t)
	c := newShieldedChain(t, shieldedAppFor(t, chainID, dbmMem(), t.TempDir(), realVerifierPath(t)), nil)
	c.fund(t, c.alice, 10_000_000)
	c.grantEarnings(t, c.aliceAddr(), 7_002) // shield-2 comes out of earnings: 7000 and two nullifier fees

	// shield-1: transparent to shielded, from alice's bank balance.
	shield1 := &shieldedtypes.MsgShield{Signer: c.aliceAddr().String(), Bundle: mustDecode(t, steps[0].Bundle)}
	requireTxOK(t, c.block(t, c.signed(t, c.alice, txGas, shield1)), 0, steps[0].Name)
	require.Equal(t, steps[1].Anchor, hex.EncodeToString(c.treeRoot(t)), "the chain's tree after shield-1 is the anchor the wallet used for shield-2")

	// shield-2: from alice's earnings.
	shield2 := &shieldedtypes.MsgShieldEarnings{Signer: c.aliceAddr().String(), Bundle: mustDecode(t, steps[1].Bundle)}
	requireTxOK(t, c.block(t, c.signed(t, c.alice, txGas, shield2)), 0, steps[1].Name)
	require.Equal(t, steps[2].Anchor, hex.EncodeToString(c.treeRoot(t)))

	// transfer: signer-less, two actions, so it declares 2 x action gas; the fee is its value balance.
	require.Equal(t, int64(100), steps[2].ValueBalance)
	transfer := &shieldedtypes.MsgShieldedTransfer{Signer: shieldedtypes.SignerlessAddress().String(), Bundle: mustDecode(t, steps[2].Bundle)}
	results := c.block(t, c.signerless(t, 2*bundleGas, transfer))
	requireTxOK(t, results, 0, steps[2].Name)
	require.Equal(t, int64(2*bundleGas), results[0].GasUsed)
	require.Equal(t, steps[3].Anchor, hex.EncodeToString(c.treeRoot(t)))
	tip, err := c.app.FeesKeeper.GetEarnings(c.app.NewContext(true), sdk.AccAddress(c.validatorAddr()))
	require.NoError(t, err)
	require.Equal(t, "78", tip.String(), "100 less 20 of base fee and 2 of nullifier fees, to the proposer")

	// unshield: bound to alice and the fee-only balance, exactly what the wallet signed for.
	unshield := &shieldedtypes.MsgUnshield{
		Signer: c.aliceAddr().String(), Bundle: mustDecode(t, steps[3].Bundle), Target: shieldedtypes.UnshieldTargetFeeTopup,
	}
	binding, err := unshield.Binding()
	require.NoError(t, err)
	require.Equal(t, steps[3].Binding, hex.EncodeToString(binding), "the wallet's binding is the chain's")
	before, err := c.app.FeesKeeper.GetFeeBalance(c.app.NewContext(true), c.aliceAddr())
	require.NoError(t, err)
	requireTxOK(t, c.block(t, c.signed(t, c.alice, txGas, unshield)), 0, steps[3].Name)
	after, err := c.app.FeesKeeper.GetFeeBalance(c.app.NewContext(true), c.aliceAddr())
	require.NoError(t, err)
	require.Equal(t, "4998", after.Sub(before).String(), "5000 unshielded less two nullifier fees, as fee-only money")

	// The books: the wallet's shielded balance is 10000 + 7000 - 100 - 5000 in the pool.
	require.Equal(t, math.NewInt(11_900).String(), c.poolBalance(t).String())
	_, count, err := c.app.ShieldedKeeper.NullifierState(c.app.NewContext(true))
	require.NoError(t, err)
	require.Equal(t, uint64(8), count, "two nullifiers per bundle, none repeated")
	for _, step := range steps {
		for _, nf := range step.Nullifiers {
			resp, err := keeper.NewQueryServerImpl(c.app.ShieldedKeeper).NullifierSpent(
				c.app.NewContextLegacy(true, cmtprotoHeader(c.height, c.blockTime(c.height))),
				&shieldedtypes.QueryNullifierSpentRequest{Nullifier: mustDecode(t, nf)})
			require.NoError(t, err)
			require.True(t, resp.Spent, "%s: %s", step.Name, nf)
		}
	}
	c.requireInvariants(t)
}

// An unshield bundle the wallet signed for one target is refused at another, by the chain.
func TestShieldedReal_theWalletUnshieldIsRefusedAtAnotherTarget(t *testing.T) {
	chainID, steps := loadWalletScenario(t)
	c := newShieldedChain(t, shieldedAppFor(t, chainID, dbmMem(), t.TempDir(), realVerifierPath(t)), nil)
	c.fund(t, c.alice, 10_000_000)
	c.grantEarnings(t, c.aliceAddr(), 7_002)
	requireTxOK(t, c.block(t, c.signed(t, c.alice, txGas, &shieldedtypes.MsgShield{Signer: c.aliceAddr().String(), Bundle: mustDecode(t, steps[0].Bundle)})), 0, "shield-1")
	requireTxOK(t, c.block(t, c.signed(t, c.alice, txGas, &shieldedtypes.MsgShieldEarnings{Signer: c.aliceAddr().String(), Bundle: mustDecode(t, steps[1].Bundle)})), 0, "shield-2")
	requireTxOK(t, c.block(t, c.signerless(t, 2*bundleGas, &shieldedtypes.MsgShieldedTransfer{Signer: shieldedtypes.SignerlessAddress().String(), Bundle: mustDecode(t, steps[2].Bundle)})), 0, "transfer")

	redirected := &shieldedtypes.MsgUnshield{
		Signer: c.aliceAddr().String(), Bundle: mustDecode(t, steps[3].Bundle),
		Target: shieldedtypes.UnshieldTargetBond, Validator: c.validatorAddr().String(),
	}
	results := c.block(t, c.signed(t, c.alice, txGas, redirected))
	require.NotZero(t, results[0].Code)
	require.Contains(t, results[0].Log, "signature rejected")
}
