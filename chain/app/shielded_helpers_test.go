package app_test

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	cmted25519 "github.com/cometbft/cometbft/crypto/ed25519"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/collections"
	"cosmossdk.io/log/v2"
	"cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app"
	"github.com/DeBrosOfficial/network/chain/app/params"
	emissiontypes "github.com/DeBrosOfficial/network/chain/x/emission/types"
	powertypes "github.com/DeBrosOfficial/network/chain/x/power/types"
	"github.com/DeBrosOfficial/network/chain/x/shielded/bundle"
	"github.com/DeBrosOfficial/network/chain/x/shielded/pool"
	shieldedtypes "github.com/DeBrosOfficial/network/chain/x/shielded/types"
)

// vectorChainID is the chain ID the committed Ironwood vectors were signed for
// (chain/x/shielded/orchardffi/testdata/chain-id). It is a localnet ID so the locked genesis
// parameters do not apply and tests can set small fees and caps; a production ID would need every
// shielded parameter at its locked value and a full bootstrap committee.
const vectorChainID = "orama-localnet-orchard-vector-1"

const shieldedTestCommittee = 30

func vectorPath(name string) string {
	return filepath.Join("..", "x", "shielded", "orchardffi", "testdata", name)
}

func loadVector(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(vectorPath(name + ".bundle"))
	require.NoError(t, err)
	return b
}

func vectorChainIDFromFile(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(vectorPath("chain-id"))
	require.NoError(t, err)
	require.Equal(t, vectorChainID, string(b), "the vectors were regenerated for another chain id")
	return string(b)
}

// shieldedApp builds an app with the shielded module for the vectors' chain ID. home may be empty
// (memory nullifier database) or a directory (persistent); verifier is the out-of-process binary.
func shieldedApp(t *testing.T, home, verifier string) *app.OramaApp {
	t.Helper()
	return shieldedAppOn(t, dbm.NewMemDB(), home, verifier)
}

// shieldedAppOn is shieldedApp over a given application database, so a test can close the app and
// open it again on the same data.
func shieldedAppOn(t *testing.T, db dbm.DB, home, verifier string) *app.OramaApp {
	t.Helper()
	return shieldedAppFor(t, vectorChainID, db, home, verifier)
}

// shieldedAppFor is shieldedAppOn for another chain ID, such as the wallet scenario's.
func shieldedAppFor(t *testing.T, chainID string, db dbm.DB, home, verifier string) *app.OramaApp {
	t.Helper()
	app.SetAddressPrefixes()
	opts := simtestutil.AppOptionsMap{}
	if home != "" {
		opts[flags.FlagHome] = home
	}
	if verifier != "" {
		opts[app.FlagShieldedVerifier] = verifier
		pin, err := app.FileSHA256(verifier)
		require.NoError(t, err)
		opts[app.FlagShieldedVerifierSHA256] = pin
	}
	oramaApp := app.NewOramaApp(log.NewNopLogger(), db, true, opts, baseapp.SetChainID(chainID))
	t.Cleanup(func() { _ = oramaApp.Close() })
	return oramaApp
}

// The keys are deterministic because the committed unshield vectors are bound to alice's address
// and to committee member 0's validator address (chain/x/shielded/orchardffi/examples/
// gen_flow_vectors.rs, which is given the same two addresses).
type shieldedChain struct {
	app       *app.OramaApp
	chainID   string
	committee []committeeKey
	genesis   time.Time
	alice     cryptotypes.PrivKey
	bob       cryptotypes.PrivKey
	height    int64
	// skew is added to every block's time, to move the chain across a 24h unshield window.
	skew time.Duration
}

func secretKey(name string) cryptotypes.PrivKey { return ed25519.GenPrivKeyFromSecret([]byte(name)) }

// newShieldedChain initializes a chain with a 30-member committee, alice as an account, and small
// shielded fees so the vectors' 100-norama transfer fee covers a transfer.
func newShieldedChain(t *testing.T, oramaApp *app.OramaApp, mutate func(*shieldedtypes.GenesisState), opts ...chainOption) *shieldedChain {
	t.Helper()
	cfg := chainConfig{minDelegation: defaultTestMinDelegation}
	for _, opt := range opts {
		opt(&cfg)
	}
	c := &shieldedChain{
		app: oramaApp, chainID: oramaApp.ChainID(), genesis: time.Unix(1_700_000_000, 0),
		alice: secretKey("orama-shielded-test-alice"), bob: secretKey("orama-shielded-test-bob"),
	}
	genState := app.NewDefaultGenesisState(oramaApp)
	members := make([]powertypes.BootstrapMember, shieldedTestCommittee)
	for i := range members {
		name := fmt.Sprintf("orama-shielded-test-committee-%d", i)
		key := committeeKey{account: secretKey(name), cons: cmted25519.GenPrivKeyFromSecret([]byte(name))}
		c.committee = append(c.committee, key)
		members[i] = powertypes.BootstrapMember{
			OperatorAddress: sdk.AccAddress(key.account.PubKey().Address()).String(),
			Moniker:         "committee",
			ConsensusPubkey: key.cons.PubKey().Bytes(),
		}
	}
	powerGen := powertypes.DefaultGenesisState()
	powerGen.Params.MinDelegationForRewards = math.NewInt(cfg.minDelegation)
	powerGen.BootstrapCommittee = members
	genState[powertypes.ModuleName] = oramaApp.AppCodec().MustMarshalJSON(powerGen)

	shieldedGen := shieldedtypes.DefaultGenesisState()
	shieldedGen.Params.NullifierFee = math.NewInt(1)
	shieldedGen.Params.ActionGas = 10
	if mutate != nil {
		mutate(shieldedGen)
	}
	genState[shieldedtypes.ModuleName] = oramaApp.AppCodec().MustMarshalJSON(shieldedGen)
	addAuthAccount(t, oramaApp, genState, c.alice)
	addAuthAccount(t, oramaApp, genState, c.bob)

	stateBytes, err := json.Marshal(genState)
	require.NoError(t, err)
	_, err = oramaApp.InitChain(&abci.RequestInitChain{
		ChainId: c.chainID, InitialHeight: 1, Time: c.genesis, AppStateBytes: stateBytes,
		ConsensusParams: &cmtproto.ConsensusParams{
			Block:     &cmtproto.BlockParams{MaxGas: 50_000_000, MaxBytes: 22_020_096},
			Evidence:  &cmtproto.EvidenceParams{MaxAgeNumBlocks: 100_000, MaxAgeDuration: time.Hour, MaxBytes: 1_048_576},
			Validator: &cmtproto.ValidatorParams{PubKeyTypes: []string{cmted25519.KeyType}},
		},
	})
	require.NoError(t, err)
	c.block(t)
	return c
}

func addr(key cryptotypes.PrivKey) sdk.AccAddress { return sdk.AccAddress(key.PubKey().Address()) }

func (c *shieldedChain) aliceAddr() sdk.AccAddress { return addr(c.alice) }

// validatorAddr is committee member 0's operator address, the validator the bond vector names.
func (c *shieldedChain) validatorAddr() sdk.ValAddress {
	return sdk.ValAddress(c.committee[0].account.PubKey().Address())
}

// block finalizes and commits the next block with these txs, proposed by committee member 0, and
// returns the tx results.
func (c *shieldedChain) block(t *testing.T, txs ...[]byte) []*abci.ExecTxResult {
	t.Helper()
	resp := c.finalize(t, txs...)
	_, err := c.app.Commit()
	require.NoError(t, err)
	return resp.TxResults
}

// finalize runs the next block and stops before Commit, as a node does that dies in between.
func (c *shieldedChain) finalize(t *testing.T, txs ...[]byte) *abci.ResponseFinalizeBlock {
	t.Helper()
	c.height++
	resp, err := c.app.FinalizeBlock(&abci.RequestFinalizeBlock{
		Height: c.height, Time: c.blockTime(c.height),
		ProposerAddress: c.committee[0].cons.PubKey().Address(), Txs: txs,
	})
	require.NoError(t, err)
	return resp
}

func (c *shieldedChain) blockTime(height int64) time.Time {
	return c.genesis.Add(time.Duration(height)*2*time.Second + c.skew)
}

// fund gives an account a public bank balance the way the other app tests do.
func (c *shieldedChain) fund(t *testing.T, who cryptotypes.PrivKey, amount int64) {
	t.Helper()
	ctx := c.app.NewNextBlockContext(cmtproto.Header{Height: c.height + 1, Time: c.blockTime(c.height + 1)})
	coins := sdk.NewCoins(sdk.NewInt64Coin(params.BaseDenom, amount))
	require.NoError(t, c.app.BankKeeper.MintCoins(ctx, emissiontypes.ModuleName, coins))
	require.NoError(t, c.app.BankKeeper.SendCoinsFromModuleToAccount(ctx, emissiontypes.ModuleName, addr(who), coins))
	writeCache(t, ctx)
	_, err := c.app.Commit()
	require.NoError(t, err)
	c.height++
}

// signed builds a signed tx of one message with gas and a fee of gas x base fee (1).
func (c *shieldedChain) signed(t *testing.T, who cryptotypes.PrivKey, gas uint64, msg sdk.Msg) []byte {
	t.Helper()
	acc := c.app.AccountKeeper.GetAccount(c.app.NewContext(true), addr(who))
	require.NotNil(t, acc)
	tx, err := simtestutil.GenSignedMockTx(
		rand.New(rand.NewSource(int64(c.height))), c.app.TxConfig(),
		[]sdk.Msg{msg}, sdk.NewCoins(sdk.NewCoin(params.BaseDenom, math.NewInt(int64(gas)))), gas, c.chainID,
		[]uint64{acc.GetAccountNumber()}, []uint64{acc.GetSequence()}, who,
	)
	require.NoError(t, err)
	b, err := c.app.TxConfig().TxEncoder()(tx)
	require.NoError(t, err)
	return b
}

// signerless builds a tx with no signature, no fee and exactly the given gas.
func (c *shieldedChain) signerless(t *testing.T, gas uint64, msg sdk.Msg) []byte {
	t.Helper()
	b := c.app.TxConfig().NewTxBuilder()
	require.NoError(t, b.SetMsgs(msg))
	b.SetGasLimit(gas)
	out, err := c.app.TxConfig().TxEncoder()(b.GetTx())
	require.NoError(t, err)
	return out
}

func poolKey() collections.Pair[uint32, []byte] {
	return collections.Join(shieldedtypes.VintageOrchardV1, pool.NativeAsset[:])
}

// shieldedTestBundle has the canonical framing up to the proof, which is all the sighash reads.
func shieldedTestBundle() []byte {
	b := make([]byte, 1+bundle.ActionLen+bundle.HeaderLen+16)
	b[0] = 1
	return b
}

// Short names the real-verifier tests share.
type (
	abciResult         = abci.ExecTxResult
	cryptotypesPrivKey = cryptotypes.PrivKey
)

func cmtprotoHeader(height int64, at time.Time) cmtproto.Header {
	return cmtproto.Header{Height: height, Time: at}
}

// defaultTestMinDelegation is x/power's minimum delegation for rewards in these tests: small
// enough that the vectors' few-hundred-norama amounts are not dust. The default is 1 ORAMA.
const defaultTestMinDelegation = 100

type chainConfig struct{ minDelegation int64 }

type chainOption func(*chainConfig)

func withMinDelegation(n int64) chainOption { return func(c *chainConfig) { c.minDelegation = n } }

func dbmMem() dbm.DB { return dbm.NewMemDB() }
