package app_test

import (
	"flag"
	"fmt"
	"math/rand"
	"sort"
	"testing"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app"
	"github.com/DeBrosOfficial/network/chain/app/params"
	emissiontypes "github.com/DeBrosOfficial/network/chain/x/emission/types"
)

// The randomized simulation (make chain-sim): a real app, driven through InitChain and FinalizeBlock
// with random valid and invalid transactions across x/bank, x/staking, x/emission's faucet and x/token,
// from a seed, so a failure is reproduced with the seed and the block count in its message. After every
// block it runs the full conservation pass (app.CheckConservationFull) and compares x/token's state
// with the model of sim_model_test.go. The chain also checks itself at the end of every block, so a
// violation halts FinalizeBlock and fails the run at the block that broke it.
var (
	simSeed   = flag.Int64("sim.seed", 1, "seed of the randomized simulation (make chain-sim runs several)")
	simBlocks = flag.Int("sim.blocks", 30, "blocks the randomized simulation produces")
	// The chain's own full conservation pass runs every min_blocks_per_epoch blocks (conservation.go).
	// 1 runs it at every height; a larger value is the production shape, where it runs at some heights
	// only. verify() runs the full pass after every block either way, so a state the chain itself would
	// not have checked is checked here.
	simProductionCadence = flag.Bool("sim.production-cadence", false, "also run the 14,400-block test that reaches the production cadence's first full conservation pass (minutes)")
	simCadence           = flag.Uint64("sim.cadence", 1, "min_blocks_per_epoch of the simulated chain, which is how often the chain runs its own full conservation pass")
)

// simSecondSeedCadence is the cadence of the second seed that every plain `go test` runs, so the default
// run covers one chain that checks itself at every block and one that checks itself at every seventh.
const simSecondSeedCadence = 7

const (
	simUserCount     = 6
	simMaxTxPerBlock = 4
	simTxGas         = uint64(3_000_000)
	simMaxGas        = int64(100_000_000)
	simUserFunds     = 1_000 * params.NoramaPerOrama
	simLowFunds      = 40 * params.NoramaPerOrama
	simMaxDrip       = 50 * params.NoramaPerOrama
)

type simUser struct {
	key  cryptotypes.PrivKey
	addr sdk.AccAddress
}

// simTx is one generated transaction and what the model says it must do.
type simTx struct {
	signer  int
	label   string
	msg     sdk.Msg
	predict simPrediction
	onOK    func()
}

type simPrediction int

const (
	simUnknown simPrediction = iota
	simMustSucceed
	simMustFail
)

type simChain struct {
	t        *testing.T
	seed     int64
	app      *app.OramaApp
	mirror   *app.OramaApp // a second app fed the same genesis and blocks; nil unless the run compares app hashes
	rng      *rand.Rand
	genesis  time.Time
	height   int64
	users    []simUser
	valoper  sdk.ValAddress
	tokens   map[string]*simToken
	created  int
	outcomes map[string]int
}

func newSimChain(t *testing.T, seed int64, withMirror bool, cadence uint64) *simChain {
	t.Helper()
	oramaApp := buildTestApp(t)
	genState, keys := committeeGenesis(t, oramaApp, 1, 365)
	emission := emissiontypes.DefaultGenesisState()
	emission.Params = emissiontypes.NewParams(2*time.Second, cadence, true)
	emission.Params.FaucetEnabled = true
	emission.Params.FaucetMaxDrip = math.NewInt(simMaxDrip)
	emission.Params.FaucetEpochCap = math.NewInt(simMaxDrip).MulRaw(1_000_000)
	emission.Params.FaucetRecipientCooldownSeconds = 0
	genState[emissiontypes.ModuleName] = oramaApp.AppCodec().MustMarshalJSON(emission)

	c := &simChain{
		t: t, seed: seed, app: oramaApp, rng: rand.New(rand.NewSource(seed)), genesis: time.Unix(1_700_000_000, 0), height: 1,
		valoper: sdk.ValAddress(keys[0].account.PubKey().Address()), tokens: map[string]*simToken{}, outcomes: map[string]int{},
	}
	for i := 0; i < simUserCount; i++ {
		key := ed25519.GenPrivKey()
		addAuthAccount(t, oramaApp, genState, key)
		c.users = append(c.users, simUser{key: key, addr: sdk.AccAddress(key.PubKey().Address())})
	}
	initChain(t, oramaApp, genState, simMaxGas, c.genesis)
	finalize(t, oramaApp, 1, c.at(1))
	if withMirror {
		c.mirror = buildTestApp(t)
		initChain(t, c.mirror, genState, simMaxGas, c.genesis)
		finalize(t, c.mirror, 1, c.at(1))
	}
	return c
}

// apps is the primary app and, when the run has one, its mirror.
func (c *simChain) apps() []*app.OramaApp {
	if c.mirror == nil {
		return []*app.OramaApp{c.app}
	}
	return []*app.OramaApp{c.app, c.mirror}
}

func (c *simChain) at(height int64) time.Time {
	return c.genesis.Add(time.Duration(height) * 2 * time.Second)
}

// topUp funds every user who is low on norama, as a write to the next block that books the coins with
// x/emission (mintNoramaForTest).
func (c *simChain) topUp() {
	ctx := c.app.NewContext(true)
	var low []sdk.AccAddress
	for _, u := range c.users {
		if c.app.BankKeeper.GetBalance(ctx, u.addr, params.BaseDenom).Amount.LT(math.NewInt(simLowFunds)) {
			low = append(low, u.addr)
		}
	}
	if len(low) == 0 {
		return
	}
	c.height++
	coins := sdk.NewCoins(sdk.NewInt64Coin(params.BaseDenom, simUserFunds))
	for _, a := range c.apps() {
		wctx := a.NewNextBlockContext(cmtproto.Header{Height: c.height, Time: c.at(c.height), ChainID: testChainID})
		for _, addr := range low {
			require.NoError(c.t, mintNoramaForTest(a, wctx, coins))
			require.NoError(c.t, a.BankKeeper.SendCoinsFromModuleToAccount(wctx, emissiontypes.ModuleName, addr, coins))
		}
		writeCache(c.t, wctx)
		_, err := a.Commit()
		require.NoError(c.t, err)
	}
}

// block generates and delivers one block, then checks it.
func (c *simChain) block() {
	c.topUp()
	txs := c.generate()
	c.height++
	raw := make([][]byte, len(txs))
	for i, tx := range txs {
		raw[i] = signedTx(c.t, c.app, c.users[tx.signer].key, c.rng.Int63(), simTxGas, tx.msg)
	}
	resp := c.deliver(c.app, raw)
	if c.mirror != nil {
		c.requireSameBlock(resp, c.deliver(c.mirror, raw))
	}
	require.Len(c.t, resp.TxResults, len(txs))
	for i, tx := range txs {
		c.settle(tx, resp.TxResults[i])
	}
	c.verify()
}

func (c *simChain) deliver(a *app.OramaApp, raw [][]byte) *abci.ResponseFinalizeBlock {
	resp, err := a.FinalizeBlock(&abci.RequestFinalizeBlock{Height: c.height, Time: c.at(c.height), Txs: raw})
	require.NoError(c.t, err, "the chain halted at height %d (seed %d)", c.height, c.seed)
	_, err = a.Commit()
	require.NoError(c.t, err)
	return resp
}

// requireSameBlock fails when two apps that ran the same block disagree on its app hash, on a
// transaction's result, gas or events, or on the validator updates.
func (c *simChain) requireSameBlock(a, b *abci.ResponseFinalizeBlock) {
	c.t.Helper()
	require.NotEmpty(c.t, a.AppHash, "height %d: no app hash", c.height)
	require.Equal(c.t, a.AppHash, b.AppHash, "seed %d height %d: app hash differs between two apps fed the same blocks", c.seed, c.height)
	require.Equal(c.t, a.ValidatorUpdates, b.ValidatorUpdates, "seed %d height %d: validator updates", c.seed, c.height)
	require.Len(c.t, b.TxResults, len(a.TxResults))
	for i := range a.TxResults {
		require.Equal(c.t, a.TxResults[i].Code, b.TxResults[i].Code, "seed %d height %d tx %d: code", c.seed, c.height, i)
		require.Equal(c.t, a.TxResults[i].GasUsed, b.TxResults[i].GasUsed, "seed %d height %d tx %d: gas used", c.seed, c.height, i)
		require.Equal(c.t, a.TxResults[i].Log, b.TxResults[i].Log, "seed %d height %d tx %d: log", c.seed, c.height, i)
		require.Equal(c.t, a.TxResults[i].Events, b.TxResults[i].Events, "seed %d height %d tx %d: events", c.seed, c.height, i)
	}
	require.Equal(c.t, a.Events, b.Events, "seed %d height %d: block events", c.seed, c.height)
}

// generate builds up to simMaxTxPerBlock transactions from distinct signers, applying each one's model
// effect before the next is generated so a later transaction sees the state an earlier one leaves.
func (c *simChain) generate() []simTx {
	n := c.rng.Intn(simMaxTxPerBlock + 1)
	signers := c.rng.Perm(simUserCount)[:n]
	txs := make([]simTx, 0, n)
	for _, s := range signers {
		txs = append(txs, c.randomTx(s))
	}
	return txs
}

// settle compares a transaction's result with the model's prediction.
func (c *simChain) settle(tx simTx, res *abci.ExecTxResult) {
	ok := res.Code == 0
	c.outcomes[fmt.Sprintf("%s ok=%t", tx.label, ok)]++
	switch tx.predict {
	case simMustSucceed:
		require.True(c.t, ok, "seed %d height %d: %s must succeed, got code %d: %s", c.seed, c.height, tx.label, res.Code, res.Log)
	case simMustFail:
		require.False(c.t, ok, "seed %d height %d: %s must fail", c.seed, c.height, tx.label)
	}
	if ok && tx.onOK != nil {
		tx.onOK()
	}
}

// verify runs the full conservation pass and compares x/token's state with the model.
func (c *simChain) verify() {
	ctx := c.app.NewContext(true)
	require.NoError(c.t, c.app.CheckConservationFull(ctx), "seed %d height %d", c.seed, c.height)
	for _, denom := range simDenoms(c.tokens) {
		tok := c.tokens[denom]
		got, err := c.app.TokenKeeper.Tokens.Get(ctx, denom)
		require.NoError(c.t, err, "seed %d height %d: token %s", c.seed, c.height, denom)
		require.True(c.t, got.Issued.Equal(tok.issued), "seed %d height %d: %s issued %s, model %s", c.seed, c.height, denom, got.Issued, tok.issued)
		require.Equal(c.t, tok.paused, got.Paused, "seed %d height %d: %s paused", c.seed, c.height, denom)
		for _, u := range c.users {
			have := c.app.BankKeeper.GetBalance(ctx, u.addr, denom).Amount
			require.True(c.t, have.Equal(tok.balance(u.addr)), "seed %d height %d: %s balance of %s is %s, model %s", c.seed, c.height, denom, u.addr, have, tok.balance(u.addr))
		}
	}
}

func (c *simChain) report() {
	keys := make([]string, 0, len(c.outcomes))
	for k := range c.outcomes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		c.t.Logf("%-28s %d", k, c.outcomes[k])
	}
}

func TestSimulation(t *testing.T) {
	t.Logf("simulation seed=%d blocks=%d", *simSeed, *simBlocks)
	c := newSimChain(t, *simSeed, false, *simCadence)
	for i := 0; i < *simBlocks; i++ {
		c.block()
	}
	c.report()
}

// A second seed on every plain `go test`, so the default run covers two interleavings.
func TestSimulation_secondSeed(t *testing.T) {
	c := newSimChain(t, *simSeed+1000, false, simSecondSeedCadence)
	for i := 0; i < *simBlocks; i++ {
		c.block()
	}
}

// Two apps fed the same genesis and the same blocks reach the same app hash at every height, and the same
// result for every transaction, however the blocks were produced: nothing in a block's execution may
// depend on map order, wall-clock time, a random source or the node. make chain-sim runs it under the
// race detector.
func TestDeterminism_twoAppsFedTheSameBlocksAgreeAtEveryHeight(t *testing.T) {
	c := newSimChain(t, *simSeed+2000, true, *simCadence)
	for i := 0; i < *simBlocks; i++ {
		c.block()
	}
	ctxA, ctxB := c.app.NewContext(true), c.mirror.NewContext(true)
	exportA, err := c.app.ExportAppStateAndValidators(false, nil, nil)
	require.NoError(t, err)
	exportB, err := c.mirror.ExportAppStateAndValidators(false, nil, nil)
	require.NoError(t, err)
	require.JSONEq(t, string(exportA.AppState), string(exportB.AppState))
	require.Equal(t, c.app.LastCommitID(), c.mirror.LastCommitID())
	require.NoError(t, c.mirror.CheckConservationFull(ctxB))
	require.NoError(t, c.app.CheckConservationFull(ctxA))
}
