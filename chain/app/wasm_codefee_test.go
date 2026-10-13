//go:build cgo && !nowasm

package app_test

import (
	"bytes"
	"compress/gzip"
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"

	wasmtypes "github.com/CosmWasm/wasmd/x/wasm/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	policytypes "github.com/DeBrosOfficial/network/chain/x/wasmpolicy/types"
)

// codeFeeSunset opens uploads a few blocks into the test chain.
const codeFeeSunset = 5

// codeFeeChain is a chain whose uploads are open, with one funded user.
func codeFeeChain(t *testing.T, opts wasmChainOptions) *wasmChain {
	t.Helper()
	opts.users, opts.sunsetHeight = 1, codeFeeSunset
	c := newWasmChain(t, opts)
	for c.height < codeFeeSunset+1 {
		c.blocks(1)
	}
	return c
}

func (c *wasmChain) supply() math.Int {
	return c.app.BankKeeper.GetSupply(c.ctx(), params.BaseDenom).Amount
}

func (c *wasmChain) cumulativeBurned() math.Int {
	state, err := c.app.EmissionKeeper.EpochState.Get(c.ctx())
	require.NoError(c.t, err)
	return state.CumulativeBurned
}

// codeFeeEvent returns the bytes and amount of the wasmpolicy_code_fee event, and whether one was emitted.
func codeFeeEvent(events []abci.Event) (nbytes, amount string, ok bool) {
	for _, ev := range events {
		if ev.Type != policytypes.EventTypeCodeFee {
			continue
		}
		for _, a := range ev.Attributes {
			switch a.Key {
			case policytypes.AttributeCodeBytes:
				nbytes = a.Value
			case policytypes.AttributeCodeFee:
				amount = a.Value
			}
		}
		return nbytes, amount, true
	}
	return "", "", false
}

func gzipWasm(t *testing.T, raw []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, err := zw.Write(raw)
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

func TestCodeFee_isSizeTimesDepositPerByteAndBurnedAndConserved(t *testing.T) {
	c := codeFeeChain(t, wasmChainOptions{})
	alice := c.users[0]

	// A transaction with the same gas and nothing to store shows what the gas fee alone does to the supply.
	supply0, burned0, bal0 := c.supply(), c.cumulativeBurned(), c.balance(alice.addr, params.BaseDenom)
	c.mustDeliver(alice, &banktypes.MsgSend{FromAddress: alice.addr.String(), ToAddress: alice.addr.String(), Amount: noramaCoins(1)})
	gasSupplyDrop, gasBurned := supply0.Sub(c.supply()), c.cumulativeBurned().Sub(burned0)
	gasBalanceDrop := bal0.Sub(c.balance(alice.addr, params.BaseDenom))

	supply1, burned1, bal1 := c.supply(), c.cumulativeBurned(), c.balance(alice.addr, params.BaseDenom)
	res := c.mustDeliver(alice, &wasmtypes.MsgStoreCode{Sender: alice.addr.String(), WASMByteCode: relayWasm})

	want := math.NewInt(int64(len(relayWasm))).MulRaw(policytypes.DefaultDepositPerByte)
	size, amount, ok := codeFeeEvent(res.Events)
	require.True(t, ok, "the upload emits the code fee event")
	require.Equal(t, math.NewInt(int64(len(relayWasm))).String(), size)
	require.Equal(t, want.String(), amount)

	require.True(t, bal1.Sub(c.balance(alice.addr, params.BaseDenom)).Equal(gasBalanceDrop.Add(want)), "the payer's bank balance pays the fee on top of gas")
	require.True(t, supply1.Sub(c.supply()).Equal(gasSupplyDrop.Add(want)), "the whole fee is burned: the supply drops by it")
	require.True(t, c.cumulativeBurned().Sub(burned1).Equal(gasBurned.Add(want)), "x/emission books the burn")
	requireConserved(t, c.app)
	c.blocks(1) // the end blocker's conservation check ran again on the next block without halting
}

func TestCodeFee_gzipIsChargedOnTheUncompressedSize(t *testing.T) {
	c := codeFeeChain(t, wasmChainOptions{})
	alice := c.users[0]
	zipped := gzipWasm(t, relayWasm)
	require.Less(t, len(zipped), len(relayWasm))

	res := c.mustDeliver(alice, &wasmtypes.MsgStoreCode{Sender: alice.addr.String(), WASMByteCode: zipped})
	size, amount, ok := codeFeeEvent(res.Events)
	require.True(t, ok)
	require.Equal(t, math.NewInt(int64(len(relayWasm))).String(), size, "wasmd gunzips, so the fee counts the wasm")
	require.Equal(t, math.NewInt(int64(len(relayWasm))).MulRaw(policytypes.DefaultDepositPerByte).String(), amount)
	requireConserved(t, c.app)
}

func TestCodeFee_aPayerWhoCannotPayStoresNothingAndPaysOnlyGas(t *testing.T) {
	// 267 KB at 5,000,000 norama a byte is far above the 1,000 ORAMA a test user holds.
	c := codeFeeChain(t, wasmChainOptions{depositPerByte: 5_000_000})
	alice := c.users[0]
	nextCode := uint64(len(c.standard.Contracts) + 1)
	burned0, bal0 := c.cumulativeBurned(), c.balance(alice.addr, params.BaseDenom)

	for name, msg := range map[string]sdk.Msg{
		"store":                 &wasmtypes.MsgStoreCode{Sender: alice.addr.String(), WASMByteCode: relayWasm},
		"store and instantiate": &wasmtypes.MsgStoreAndInstantiateContract{Authority: alice.addr.String(), WASMByteCode: relayWasm, Label: "x", Msg: []byte(`{}`)},
		"store and migrate":     &wasmtypes.MsgStoreAndMigrateContract{Authority: alice.addr.String(), WASMByteCode: relayWasm, Contract: alice.addr.String(), Msg: []byte(`{}`)},
	} {
		res := c.deliver(alice, msg)
		require.NotZero(t, res.Code, name)
		require.Contains(t, res.Log, "code fee cannot be charged", name)
		_, _, charged := codeFeeEvent(res.Events)
		require.False(t, charged, name)
	}
	require.Nil(t, c.app.WasmKeeper().GetCodeInfo(c.ctx(), nextCode), "no code was stored")

	// Three refused transactions cost their gas fee and nothing else.
	require.Equal(t, math.NewInt(3*int64(wasmGas)).String(), bal0.Sub(c.balance(alice.addr, params.BaseDenom)).String())
	require.True(t, c.cumulativeBurned().Sub(burned0).LTE(math.NewInt(3*int64(wasmGas))), "only gas fees were burned")
	requireConserved(t, c.app)
}

func TestCodeFee_storeAndInstantiateIsCharged(t *testing.T) {
	c := codeFeeChain(t, wasmChainOptions{})
	alice := c.users[0]
	res := c.mustDeliver(alice, &wasmtypes.MsgStoreAndInstantiateContract{Authority: alice.addr.String(), WASMByteCode: relayWasm, Label: "x", Msg: []byte(`{}`)})
	size, amount, ok := codeFeeEvent(res.Events)
	require.True(t, ok)
	require.Equal(t, math.NewInt(int64(len(relayWasm))).String(), size)
	require.Equal(t, math.NewInt(int64(len(relayWasm))).MulRaw(policytypes.DefaultDepositPerByte).String(), amount)
	requireConserved(t, c.app)
}

func TestCodeFee_simulationReportsTheFee(t *testing.T) {
	c := codeFeeChain(t, wasmChainOptions{})
	alice := c.users[0]
	tx := signedTx(t, c.app, alice.key, int64(c.height+1), wasmGas, &wasmtypes.MsgStoreCode{Sender: alice.addr.String(), WASMByteCode: relayWasm})
	_, result, err := c.app.Simulate(tx)
	require.NoError(t, err)
	var found bool
	for _, ev := range result.Events {
		if ev.Type == policytypes.EventTypeCodeFee {
			found = true
		}
	}
	require.True(t, found, "a simulated upload carries the code fee event")
	require.Nil(t, c.app.WasmKeeper().GetCodeInfo(c.ctx(), uint64(len(c.standard.Contracts)+1)), "a simulation stores nothing")
}

func TestCodeFee_genesisContractsAreNotCharged(t *testing.T) {
	c := newWasmChain(t, wasmChainOptions{users: 1})
	require.NotEmpty(t, c.standard.Contracts)
	for id := uint64(1); id <= uint64(len(c.standard.Contracts)); id++ {
		require.NotNil(t, c.app.WasmKeeper().GetCodeInfo(c.ctx(), id))
	}
	burned, err := c.app.EmissionKeeper.EpochState.Get(c.ctx())
	require.NoError(t, err)
	require.True(t, burned.CumulativeBurned.IsZero(), "storing the standard contracts at genesis burned nothing")
}
