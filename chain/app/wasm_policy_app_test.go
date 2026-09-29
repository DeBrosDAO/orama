//go:build cgo && !nowasm

package app_test

import (
	"encoding/json"
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	wasmtypes "github.com/CosmWasm/wasmd/x/wasm/types"

	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	policytypes "github.com/DeBrosOfficial/network/chain/x/wasmpolicy/types"
)

// relayChain is a chain with funded users and one relay contract that nobody funded.
type relayChain struct {
	*wasmChain
	alice, bob wasmUser
	relay      sdk.AccAddress
	codeID     uint64
}

func newRelayChain(t *testing.T, opts wasmChainOptions) *relayChain {
	t.Helper()
	if opts.users == 0 {
		opts.users = 2
	}
	c := newWasmChain(t, opts)
	id := c.storeRelay()
	return &relayChain{wasmChain: c, alice: c.users[0], bob: c.users[1], relay: c.instantiate(c.users[0], id, map[string]any{}, nil), codeID: id}
}

func (r *relayChain) store(user wasmUser, contract sdk.AccAddress, key string, size int) abci.ExecTxResult {
	r.t.Helper()
	return r.exec(user, contract, map[string]any{"store_bytes": map[string]any{"key": key, "len": size}}, nil)
}

func (r *relayChain) charged(contract sdk.AccAddress) uint64 {
	r.t.Helper()
	n, err := r.app.WasmPolicyKeeper.ChargedBytes(r.ctx(), contract)
	require.NoError(r.t, err)
	return n
}

func (r *relayChain) requireInvariants() {
	r.t.Helper()
	inv, err := r.app.WasmPolicyKeeper.CheckInvariants(r.ctx())
	require.NoError(r.t, err)
	require.True(r.t, inv.BytesMatch && inv.DepositsMatch, inv.Detail)
	fees, err := r.app.FeesKeeper.CheckInvariants(r.ctx())
	require.NoError(r.t, err)
	require.True(r.t, fees.EarningsMatchModule && fees.DepositsMatchModule && fees.FeesBalance, fees.Detail)
}

func TestDeposit_growthIsChargedToTheCallerAndDeletionRefunds(t *testing.T) {
	r := newRelayChain(t, wasmChainOptions{})
	perByte := math.NewInt(policytypes.DefaultDepositPerByte)
	require.Zero(t, r.charged(r.relay), "instantiating a contract that stores nothing charges nothing")

	res := r.store(r.bob, r.relay, "k1", 100)
	require.Zero(t, res.Code, res.Log)
	const stored = 2 + 100 // key plus value
	require.Equal(t, uint64(stored), r.charged(r.relay))
	dep, err := r.app.FeesKeeper.GetDeposit(r.ctx(), policytypes.DepositID(r.relay.String(), 0))
	require.NoError(t, err)
	require.Equal(t, r.bob.addr.String(), dep.Owner, "the caller, not the contract or its creator, locked the deposit")
	require.True(t, dep.Amount.Equal(perByte.MulRaw(stored)))
	r.requireInvariants()

	// Overwriting with a shorter value frees the difference, released newest chunk first.
	require.Zero(t, r.store(r.bob, r.relay, "k1", 60).Code)
	require.Equal(t, uint64(2+60), r.charged(r.relay))
	dep, err = r.app.FeesKeeper.GetDeposit(r.ctx(), policytypes.DepositID(r.relay.String(), 0))
	require.NoError(t, err)
	require.True(t, dep.Amount.Equal(perByte.MulRaw(62)))
	r.requireInvariants()

	// Deleting the key releases everything: 99% goes to the payer's earnings, 1% is burned.
	before, err := r.app.FeesKeeper.GetEarnings(r.ctx(), r.bob.addr)
	require.NoError(t, err)
	r.mustExec(r.alice, r.relay, map[string]any{"remove": map[string]any{"key": "k1"}}, nil)
	require.Zero(t, r.charged(r.relay))
	_, err = r.app.FeesKeeper.GetDeposit(r.ctx(), policytypes.DepositID(r.relay.String(), 0))
	require.Error(t, err, "the deposit is gone")
	after, err := r.app.FeesKeeper.GetEarnings(r.ctx(), r.bob.addr)
	require.NoError(t, err)
	want := perByte.MulRaw(62).MulRaw(99).QuoRaw(100)
	require.True(t, after.Sub(before).Equal(want), "refund %s, want %s to the original payer, not the caller who deleted", after.Sub(before), want)
	r.requireInvariants()
}

func TestDeposit_eachPayerGetsTheirOwnRefund(t *testing.T) {
	r := newRelayChain(t, wasmChainOptions{})
	require.Zero(t, r.store(r.alice, r.relay, "a", 10).Code)
	require.Zero(t, r.store(r.bob, r.relay, "b", 30).Code)
	r.requireInvariants()

	aliceBefore, err := r.app.FeesKeeper.GetEarnings(r.ctx(), r.alice.addr)
	require.NoError(t, err)
	bobBefore, err := r.app.FeesKeeper.GetEarnings(r.ctx(), r.bob.addr)
	require.NoError(t, err)
	r.mustExec(r.alice, r.relay, map[string]any{"remove": map[string]any{"key": "b"}}, nil)

	aliceAfter, err := r.app.FeesKeeper.GetEarnings(r.ctx(), r.alice.addr)
	require.NoError(t, err)
	bobAfter, err := r.app.FeesKeeper.GetEarnings(r.ctx(), r.bob.addr)
	require.NoError(t, err)
	require.True(t, aliceAfter.Equal(aliceBefore), "alice deleted bob's entry and is not refunded for it")
	require.True(t, bobAfter.GT(bobBefore), "bob is")
	require.Equal(t, uint64(1+10), r.charged(r.relay), "alice's own entry is still charged")
	r.requireInvariants()
}

func TestDeposit_theSignerPaysForStateAnInnerContractCallGrows(t *testing.T) {
	r := newRelayChain(t, wasmChainOptions{})
	inner := r.instantiate(r.alice, r.codeID, map[string]any{}, nil)
	msg, err := json.Marshal(map[string]any{"store_bytes": map[string]any{"key": "deep", "len": 20}})
	require.NoError(t, err)
	call := map[string]any{"wasm": map[string]any{"execute": map[string]any{"contract_addr": inner.String(), "msg": msg, "funds": []any{}}}}
	r.mustExec(r.bob, r.relay, map[string]any{"dispatch": map[string]any{"msgs": []any{call}}}, nil)

	require.Equal(t, uint64(4+20), r.charged(inner))
	require.Zero(t, r.charged(r.relay))
	dep, err := r.app.FeesKeeper.GetDeposit(r.ctx(), policytypes.DepositID(inner.String(), 0))
	require.NoError(t, err)
	require.Equal(t, r.bob.addr.String(), dep.Owner, "the outer contract called; the signer pays, not the contract")
	r.requireInvariants()
}

func TestDeposit_aCallThatCannotPayFailsAndChargesNothing(t *testing.T) {
	// One norama per byte would be affordable; a trillion per byte is not.
	r := newRelayChain(t, wasmChainOptions{depositPerByte: 1_000_000_000_000})
	res := r.store(r.bob, r.relay, "big", 100)
	require.NotZero(t, res.Code)
	require.Contains(t, res.Log, "state deposit")
	require.Zero(t, r.charged(r.relay))
	got, err := r.app.WasmKeeper().QuerySmart(r.ctx(), r.relay, []byte(`{"get":{"key":"big"}}`))
	require.NoError(t, err)
	require.Empty(t, got, "the write was rolled back")
	r.requireInvariants()
}

func TestDeposit_aRevertedTransactionLeavesNoLedgerEntry(t *testing.T) {
	r := newRelayChain(t, wasmChainOptions{})
	store := map[string]any{"wasm": map[string]any{"execute": map[string]any{"contract_addr": r.relay.String(), "msg": []byte(`{"store":{"key":"k","value":"v"}}`), "funds": []any{}}}}
	bad := map[string]any{"bank": map[string]any{"send": map[string]any{"to_address": r.bob.addr.String(), "amount": []map[string]string{{"denom": params.BaseDenom, "amount": "1"}}}}}
	res := r.exec(r.alice, r.relay, map[string]any{"dispatch": map[string]any{"msgs": []any{store, bad}}}, nil)
	require.NotZero(t, res.Code)
	require.Zero(t, r.charged(r.relay))
	r.requireInvariants()
}

func TestDeposit_ledgerSurvivesAGenesisExport(t *testing.T) {
	r := newRelayChain(t, wasmChainOptions{})
	require.Zero(t, r.store(r.bob, r.relay, "k", 50).Code)

	exported, err := r.app.ExportAppStateAndValidators(false, nil, nil)
	require.NoError(t, err)
	var state map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(exported.AppState, &state))
	var policy policytypes.GenesisState
	require.NoError(t, json.Unmarshal(state[policytypes.ModuleName], &policy))
	require.NoError(t, policy.Validate())
	require.Len(t, policy.DepositChunks, 1)
	require.Equal(t, uint64(1+50), policy.DepositChunks[0].Bytes)
	require.Equal(t, r.bob.addr.String(), policy.DepositChunks[0].Payer)
}

func TestUpload_isRefusedBeforeTheSunsetHeightAndAllowedAtIt(t *testing.T) {
	const sunset = 40
	c := newWasmChain(t, wasmChainOptions{users: 1, sunsetHeight: sunset})
	alice := c.users[0]
	store := func() abci.ExecTxResult {
		return c.deliver(alice, &wasmtypes.MsgStoreCode{Sender: alice.addr.String(), WASMByteCode: relayWasm})
	}

	before := store()
	require.NotZero(t, before.Code)
	require.Contains(t, before.Log, "code upload is closed")

	// Even a byte-for-byte copy of a genesis contract is refused: an upload names no code id.
	c.blocks(1)
	res := c.deliver(alice, &wasmtypes.MsgStoreCode{Sender: alice.addr.String(), WASMByteCode: c.standard.Contracts[0].Wasm})
	require.NotZero(t, res.Code)
	require.Contains(t, res.Log, "code upload is closed")

	// The other messages that upload code are closed the same way.
	c.blocks(1)
	for name, msg := range map[string]sdk.Msg{
		"store and instantiate": &wasmtypes.MsgStoreAndInstantiateContract{Authority: alice.addr.String(), WASMByteCode: relayWasm, Label: "x", Msg: []byte(`{}`)},
		"store and migrate":     &wasmtypes.MsgStoreAndMigrateContract{Authority: alice.addr.String(), WASMByteCode: relayWasm, Contract: alice.addr.String(), Msg: []byte(`{}`)},
	} {
		res := c.deliver(alice, msg)
		require.NotZero(t, res.Code, name)
		require.Contains(t, res.Log, "code upload is closed", name)
	}

	for c.height < sunset-2 {
		c.blocks(1)
	}
	last := store() // finalized at height sunset-1
	require.NotZero(t, last.Code, "the block before the sunset height is still closed")

	atSunset := store() // finalized at height sunset
	require.Zero(t, atSunset.Code, "the sunset height itself is open: %s", atSunset.Log)
	require.NotNil(t, c.app.WasmKeeper().GetCodeInfo(c.ctx(), uint64(len(c.standard.Contracts)+1)), "the new code is stored after the standard ones")
}

func TestUpload_theHeightCannotBeChangedByAnyMessage(t *testing.T) {
	c := newWasmChain(t, wasmChainOptions{users: 1})
	before, err := c.app.WasmPolicyKeeper.SunsetHeight(c.ctx())
	require.NoError(t, err)
	c.blocks(3)
	after, err := c.app.WasmPolicyKeeper.SunsetHeight(c.ctx())
	require.NoError(t, err)
	require.Equal(t, before, after)
	require.Equal(t, policytypes.DefaultUploadSunsetHeight, after)
}

func TestContracts_submessageRepliesAndReentrancy(t *testing.T) {
	r := newRelayChain(t, wasmChainOptions{})
	self := func(msg string) map[string]any {
		return map[string]any{"wasm": map[string]any{"execute": map[string]any{"contract_addr": r.relay.String(), "msg": []byte(msg), "funds": []any{}}}}
	}

	// The contract calls itself as a submessage: it re-enters, writes, and gets a success reply.
	r.mustExec(r.alice, r.relay, map[string]any{"dispatch_sub": map[string]any{"id": 7, "msg": self(`{"store":{"key":"inner","value":"1"}}`)}}, nil)
	require.Equal(t, "ok:7", string(r.smart(r.relay, map[string]any{"get": map[string]string{"key": "last_reply"}})))
	require.Equal(t, "1", string(r.smart(r.relay, map[string]any{"get": map[string]string{"key": "inner"}})))

	// A failing submessage does not fail the transaction; the reply records the error, and the
	// failed call's writes are gone.
	bad := map[string]any{"bank": map[string]any{"send": map[string]any{"to_address": r.bob.addr.String(), "amount": []map[string]string{{"denom": params.BaseDenom, "amount": "1"}}}}}
	r.mustExec(r.alice, r.relay, map[string]any{"dispatch_sub": map[string]any{"id": 8, "msg": bad}}, nil)
	require.Contains(t, string(r.smart(r.relay, map[string]any{"get": map[string]string{"key": "last_reply"}})), "err:8:")
	r.requireInvariants()
}

func TestContracts_gasExhaustionAndUnboundedRecursionFailTheTransaction(t *testing.T) {
	r := newRelayChain(t, wasmChainOptions{})
	spin := r.deliverGas(r.alice, 2_000_000, r.execMsg(r.alice, r.relay, map[string]any{"spin": map[string]any{}}, nil))
	require.NotZero(t, spin.Code)
	require.Contains(t, spin.Log, "out of gas")

	// A contract that calls itself without end runs into the call-depth limit or the gas limit.
	recurse := map[string]any{"wasm": map[string]any{"execute": map[string]any{"contract_addr": r.relay.String(), "msg": []byte(`{"spin":{}}`), "funds": []any{}}}}
	res := r.deliverGas(r.alice, 3_000_000, r.execMsg(r.alice, r.relay, map[string]any{"dispatch": map[string]any{"msgs": []any{recurse}}}, nil))
	require.NotZero(t, res.Code)
	require.Zero(t, r.charged(r.relay))
}

// TestContracts_aUserCanInstantiateWithNoramaAttached covers wasmd's instantiate: it moves the
// attached funds before it registers the new contract, so the norama send restriction has to
// treat the recipient as the contract it is becoming. A user still cannot send norama to a plain
// address, or to a contract address before it exists.
func TestContracts_aUserCanInstantiateWithNoramaAttached(t *testing.T) {
	r := newRelayChain(t, wasmChainOptions{})
	funded := r.instantiate(r.bob, r.codeID, map[string]any{}, norama(7*params.NoramaPerOrama))
	require.True(t, r.balance(funded, params.BaseDenom).Equal(math.NewInt(7*params.NoramaPerOrama)))

	// Attaching funds to an execute of an existing contract works too.
	r.mustExec(r.bob, funded, map[string]any{"store": map[string]string{"key": "x", "value": "y"}}, norama(1))

	// A plain user address is still not payable.
	res := r.deliver(r.bob, &banktypes.MsgSend{FromAddress: r.bob.addr.String(), ToAddress: r.alice.addr.String(), Amount: norama(1)})
	require.NotZero(t, res.Code)
	require.Contains(t, res.Log, "user-to-user norama transfer is refused")
}
