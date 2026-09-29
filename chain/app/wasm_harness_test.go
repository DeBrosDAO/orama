//go:build cgo && !nowasm

package app_test

import (
	_ "embed"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	wasmtypes "github.com/CosmWasm/wasmd/x/wasm/types"

	"github.com/DeBrosOfficial/network/chain/app"
	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/contracts/standard"
	emissiontypes "github.com/DeBrosOfficial/network/chain/x/emission/types"
	policytypes "github.com/DeBrosOfficial/network/chain/x/wasmpolicy/types"
)

//go:embed testdata/relay.wasm
var relayWasm []byte

// wasmGas is the gas limit of a test transaction. The base fee is 1 norama per gas, so this is
// also the fee.
const wasmGas = uint64(8_000_000)

// wasmUser is a funded account that can sign.
type wasmUser struct {
	key  cryptotypes.PrivKey
	addr sdk.AccAddress
}

// wasmChain is a real app with the standard contracts in genesis, driven through FinalizeBlock.
type wasmChain struct {
	t        *testing.T
	app      *app.OramaApp
	genesis  time.Time
	height   int64
	standard standard.Manifest
	users    []wasmUser
}

type wasmChainOptions struct {
	users          int
	sunsetHeight   uint64
	skipStandard   bool
	depositPerByte int64
}

// newWasmChain builds the chain: a one-member committee, opts.users funded accounts, and (unless
// skipStandard) the standard contracts added exactly as `oramad genesis add-standard-contracts` does.
func newWasmChain(t *testing.T, opts wasmChainOptions) *wasmChain {
	t.Helper()
	oramaApp := buildTestApp(t)
	genState, _ := committeeGenesis(t, oramaApp, 1, 365)
	c := &wasmChain{t: t, app: oramaApp, genesis: time.Unix(1_700_000_000, 0), height: 1}
	for i := 0; i < opts.users; i++ {
		key := ed25519.GenPrivKey()
		addAuthAccount(t, oramaApp, genState, key)
		c.users = append(c.users, wasmUser{key: key, addr: sdk.AccAddress(key.PubKey().Address())})
	}
	if !opts.skipStandard {
		require.NoError(t, standard.Apply(genState))
		m, err := standard.Load()
		require.NoError(t, err)
		c.standard = m
	}
	var policy policytypes.GenesisState
	require.NoError(t, json.Unmarshal(genState[policytypes.ModuleName], &policy))
	if opts.sunsetHeight != 0 {
		policy.UploadSunsetHeight = opts.sunsetHeight
	}
	if opts.depositPerByte != 0 {
		policy.DepositPerByte = math.NewInt(opts.depositPerByte)
	}
	raw, err := json.Marshal(policy)
	require.NoError(t, err)
	genState[policytypes.ModuleName] = raw

	initChain(t, oramaApp, genState, 100_000_000, c.genesis)
	finalize(t, oramaApp, 1, c.at(1))
	for _, u := range c.users {
		c.fund(u.addr, 1_000*params.NoramaPerOrama)
	}
	return c
}

func (c *wasmChain) at(height int64) time.Time {
	return c.genesis.Add(time.Duration(height) * 2 * time.Second)
}

// write runs fn in the next block's context and commits it as that block.
func (c *wasmChain) write(fn func(ctx sdk.Context)) {
	c.t.Helper()
	c.height++
	ctx := c.app.NewNextBlockContext(cmtproto.Header{Height: c.height, Time: c.at(c.height), ChainID: testChainID})
	fn(ctx)
	writeCache(c.t, ctx)
	_, err := c.app.Commit()
	require.NoError(c.t, err)
}

// fund mints norama from x/emission's module account and sends it to addr, which may be a contract.
func (c *wasmChain) fund(addr sdk.AccAddress, norama int64) {
	c.t.Helper()
	c.write(func(ctx sdk.Context) {
		coins := sdk.NewCoins(sdk.NewCoin(params.BaseDenom, math.NewInt(norama)))
		require.NoError(c.t, c.app.BankKeeper.MintCoins(ctx, emissiontypes.ModuleName, coins))
		require.NoError(c.t, c.app.BankKeeper.SendCoinsFromModuleToAccount(ctx, emissiontypes.ModuleName, addr, coins))
	})
}

// deliver signs msgs as user and finalizes them as one block. It returns the transaction result.
func (c *wasmChain) deliver(user wasmUser, msgs ...sdk.Msg) abci.ExecTxResult {
	c.t.Helper()
	return c.deliverGas(user, wasmGas, msgs...)
}

func (c *wasmChain) deliverGas(user wasmUser, gas uint64, msgs ...sdk.Msg) abci.ExecTxResult {
	c.t.Helper()
	c.height++
	tx := signedTx(c.t, c.app, user.key, int64(c.height), gas, msgs...)
	resp := finalize(c.t, c.app, c.height, c.at(c.height), tx)
	require.Len(c.t, resp.TxResults, 1)
	return *resp.TxResults[0]
}

// mustDeliver is deliver for a transaction that has to succeed.
func (c *wasmChain) mustDeliver(user wasmUser, msgs ...sdk.Msg) abci.ExecTxResult {
	c.t.Helper()
	res := c.deliver(user, msgs...)
	require.Zero(c.t, res.Code, "tx failed: %s", res.Log)
	return res
}

// blocks finalizes n empty blocks.
func (c *wasmChain) blocks(n int) {
	c.t.Helper()
	for i := 0; i < n; i++ {
		c.height++
		finalize(c.t, c.app, c.height, c.at(c.height))
	}
}

// ctx is a read-only context at the last committed block, with that block's time so contract
// queries have an environment.
func (c *wasmChain) ctx() sdk.Context {
	return c.app.NewContext(true).WithBlockHeight(c.height).WithBlockTime(c.at(c.height))
}

func (c *wasmChain) instantiate(user wasmUser, codeID uint64, msg any, funds sdk.Coins) sdk.AccAddress {
	c.t.Helper()
	raw, err := json.Marshal(msg)
	require.NoError(c.t, err)
	res := c.mustDeliver(user, &wasmtypes.MsgInstantiateContract{
		Sender: user.addr.String(), CodeID: codeID, Label: "test-" + strconv.FormatUint(codeID, 10), Msg: raw, Funds: funds,
	})
	return contractFromEvents(c.t, res)
}

func contractFromEvents(t *testing.T, res abci.ExecTxResult) sdk.AccAddress {
	t.Helper()
	for _, ev := range res.Events {
		if ev.Type != "instantiate" {
			continue
		}
		for _, a := range ev.Attributes {
			if a.Key == "_contract_address" {
				addr, err := sdk.AccAddressFromBech32(a.Value)
				require.NoError(t, err)
				return addr
			}
		}
	}
	t.Fatalf("no instantiate event in %v", res.Events)
	return nil
}

func (c *wasmChain) execMsg(user wasmUser, contract sdk.AccAddress, msg any, funds sdk.Coins) *wasmtypes.MsgExecuteContract {
	c.t.Helper()
	raw, err := json.Marshal(msg)
	require.NoError(c.t, err)
	return &wasmtypes.MsgExecuteContract{Sender: user.addr.String(), Contract: contract.String(), Msg: raw, Funds: funds}
}

func (c *wasmChain) exec(user wasmUser, contract sdk.AccAddress, msg any, funds sdk.Coins) abci.ExecTxResult {
	c.t.Helper()
	return c.deliver(user, c.execMsg(user, contract, msg, funds))
}

func (c *wasmChain) mustExec(user wasmUser, contract sdk.AccAddress, msg any, funds sdk.Coins) abci.ExecTxResult {
	c.t.Helper()
	res := c.exec(user, contract, msg, funds)
	require.Zero(c.t, res.Code, "execute failed: %s", res.Log)
	return res
}

// smart queries a contract and returns the raw JSON answer.
func (c *wasmChain) smart(contract sdk.AccAddress, query any) []byte {
	c.t.Helper()
	raw, err := json.Marshal(query)
	require.NoError(c.t, err)
	out, err := c.app.WasmKeeper().QuerySmart(c.ctx(), contract, raw)
	require.NoError(c.t, err)
	return out
}

func noramaCoins(n int64) sdk.Coins {
	return sdk.NewCoins(sdk.NewCoin(params.BaseDenom, math.NewInt(n)))
}

// relay stores the test relay contract through the keeper (its code id is returned) so a test can
// instantiate it. Storing bypasses the upload sunset the way a genesis import does, and is only
// done here, in a test.
func (c *wasmChain) storeRelay() uint64 {
	c.t.Helper()
	var id uint64
	c.write(func(ctx sdk.Context) {
		var err error
		id, _, err = c.app.WasmContractKeeper().Create(ctx, c.users[0].addr, relayWasm, nil)
		require.NoError(c.t, err)
	})
	return id
}

func moduleAddress(name string) sdk.AccAddress { return authtypes.NewModuleAddress(name) }
