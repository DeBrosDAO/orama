//go:build cgo && !nowasm

package app_test

import (
	_ "embed"
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/token/types"
)

//go:embed testdata/hook.wasm
var hookWasm []byte

// storeHook stores the test hook contract (chain/app/testdata/hook) through the keeper and
// instantiates it, the way a genesis import would; uploads are closed on a real chain.
func (c *wasmChain) storeHook() sdk.AccAddress {
	c.t.Helper()
	var id uint64
	c.write(func(ctx sdk.Context) {
		var err error
		id, _, err = c.app.WasmContractKeeper().Create(ctx, c.users[0].addr, hookWasm, nil)
		require.NoError(c.t, err)
	})
	return c.instantiate(c.users[0], id, map[string]any{}, nil)
}

// A token that names a contract at creation has it called on every MsgTransfer, with the
// transfer's denom, parties and amount: the contract allows, refuses, or runs out of the hook's
// gas cap, and a refused or exhausted transfer moves nothing.
func TestTokenTransferHook_aContractAllowsRefusesAndRunsOutOfGas(t *testing.T) {
	c := newWasmChain(t, wasmChainOptions{users: 3})
	creator, holder, other := c.users[0], c.users[1], c.users[2]
	hook := c.storeHook()

	res := c.deliver(creator, &types.MsgCreateToken{
		Creator: creator.addr.String(), Subdenom: "hooked", Name: "Hooked", Symbol: "HK", Mint: true, TransferHook: hook.String(),
	})
	require.Zero(t, res.Code, res.Log)
	denom := types.Denom(creator.addr.String(), "hooked")
	c.mustDeliver(creator, &types.MsgMint{Sender: creator.addr.String(), Denom: denom, Recipient: holder.addr.String(), Amount: math.NewInt(1000)})

	transfer := func(amount int64) {
		t.Helper()
		res = c.deliver(holder, &types.MsgTransfer{
			Sender: holder.addr.String(), From: holder.addr.String(), To: other.addr.String(), Denom: denom, Amount: math.NewInt(amount),
		})
	}
	balance := func(u wasmUser) math.Int {
		return c.app.BankKeeper.GetBalance(c.ctx(), u.addr, denom).Amount
	}

	transfer(5)
	require.Zero(t, res.Code, "an amount the contract allows moves: %s", res.Log)
	require.True(t, balance(other).Equal(math.NewInt(5)))

	transfer(13)
	require.NotZero(t, res.Code)
	require.Contains(t, res.Log, "this hook refuses a transfer of 13", "the contract's own reason reaches the sender")
	require.True(t, balance(other).Equal(math.NewInt(5)), "a refused transfer moves nothing")

	transfer(99)
	require.NotZero(t, res.Code)
	require.Contains(t, res.Log, "transfer hook exceeded gas cap", "a contract that loops is stopped by the hook's gas cap")
	require.True(t, balance(other).Equal(math.NewInt(5)), "an exhausted hook moves nothing")
	require.True(t, balance(holder).Equal(math.NewInt(995)))
}

// Naming a contract that does not exist, or one that is no contract, is refused at creation.
func TestTokenTransferHook_theContractMustExistAtCreation(t *testing.T) {
	c := newWasmChain(t, wasmChainOptions{users: 2})
	creator := c.users[0]
	for name, hook := range map[string]string{
		"an account that is no contract": c.users[1].addr.String(),
		"an address that does not exist": sdk.AccAddress(bytesRepeat(0x42)).String(),
		"not an address":                 "orama1notanaddress",
	} {
		res := c.deliver(creator, &types.MsgCreateToken{Creator: creator.addr.String(), Subdenom: "bad", Name: "Bad", Symbol: "BD", TransferHook: hook})
		require.NotZero(t, res.Code, name)
	}
}
