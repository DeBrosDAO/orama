package app_test

import (
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"

	tokentypes "github.com/DeBrosOfficial/network/chain/x/token/types"
)

// A token's powers are bank-level rules: a plain x/bank MsgSend is held to them
// exactly like MsgTransfer, so no one steps around a freeze, a pause, a
// non-transferable flag or a transfer fee by sending through x/bank.
func TestWalletFlow_tokenPowersHoldOnABankSend(t *testing.T) {
	f := newFlow(t)
	alice, bob := f.newWallet(), f.newWallet()
	for _, w := range []wallet{alice, bob} {
		f.fundEarnings(w, flowCredit)
	}
	f.fundBank(alice, flowCredit)
	send := func(from, to wallet, denom string) *abci.ExecTxResult {
		return f.deliver(from, banktypes.NewMsgSend(from.addr, to.addr, sdk.NewCoins(sdk.NewInt64Coin(denom, 1))))
	}
	mint := func(denom string, to wallet, n int64) {
		requireOK(t, f.deliver(alice, &tokentypes.MsgMint{Sender: alice.addr.String(), Denom: denom, Recipient: to.addr.String(), Amount: math.NewInt(n)}))
	}

	ice := f.createToken(alice, "ice", &tokentypes.MsgCreateToken{Mint: true, Freeze: true, Pause: true})
	mint(ice, bob, 10)
	requireOK(t, send(bob, alice, ice))

	requireOK(t, f.deliver(alice, &tokentypes.MsgSetFrozen{Sender: alice.addr.String(), Denom: ice, Account: bob.addr.String(), Frozen: true}))
	require.NotZero(t, send(bob, alice, ice).Code, "a frozen holder cannot step around the freeze with a bank send")
	requireOK(t, f.deliver(alice, &tokentypes.MsgSetFrozen{Sender: alice.addr.String(), Denom: ice, Account: bob.addr.String(), Frozen: false}))

	requireOK(t, f.deliver(alice, &tokentypes.MsgSetPaused{Sender: alice.addr.String(), Denom: ice, Paused: true}))
	require.NotZero(t, send(bob, alice, ice).Code, "a paused token does not move by bank send")
	requireOK(t, f.deliver(alice, &tokentypes.MsgSetPaused{Sender: alice.addr.String(), Denom: ice, Paused: false}))
	requireOK(t, send(bob, alice, ice))

	badge := f.createToken(alice, "badge", &tokentypes.MsgCreateToken{Mint: true, NonTransferable: true})
	mint(badge, alice, 5)
	require.NotZero(t, send(alice, bob, badge).Code, "a non-transferable token does not move by bank send")

	taxed := f.createToken(alice, "taxed", &tokentypes.MsgCreateToken{Mint: true, TransferFeeBps: 100})
	mint(taxed, alice, 5)
	require.NotZero(t, send(alice, bob, taxed).Code, "a token with a transfer fee moves only by MsgTransfer")

	plain := f.createToken(alice, "plain", &tokentypes.MsgCreateToken{Mint: true})
	mint(plain, alice, 5)
	requireOK(t, send(alice, bob, plain))
	require.Equal(t, "1", f.bank(bob.addr, plain).String())
	f.requireInvariants()
}
