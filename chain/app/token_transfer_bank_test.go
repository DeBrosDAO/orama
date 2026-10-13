package app_test

import (
	"context"
	"errors"
	"testing"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	tokentypes "github.com/DeBrosOfficial/network/chain/x/token/types"
)

// tokenTransferFixture is a real chain (real bank keeper, real store, real send restrictions) with one
// token that has a transfer fee and a holder of 10,000 of it.
type tokenTransferFixture struct {
	chain  *conservationChain
	ctx    sdk.Context
	denom  string
	holder sdk.AccAddress
	other  sdk.AccAddress
}

func newTokenTransferFixture(t *testing.T) *tokenTransferFixture {
	t.Helper()
	c := newConservationChain(t, 1)
	c.height++
	ctx := c.app.NewNextBlockContext(cmtproto.Header{Height: c.height, Time: c.at(c.height), ChainID: testChainID})
	creator := sdk.AccAddress([]byte("token_transfer_creat"))
	holder := sdk.AccAddress([]byte("token_transfer_holde"))
	other := sdk.AccAddress([]byte("token_transfer_other"))

	require.NoError(t, mintNoramaForTest(c.app, ctx, sdk.NewCoins(sdk.NewInt64Coin(params.BaseDenom, 1_000_000_000_000_000))))
	require.NoError(t, c.app.BankKeeper.SendCoinsFromModuleToAccount(ctx, "emission", creator,
		sdk.NewCoins(sdk.NewInt64Coin(params.BaseDenom, 1_000_000_000_000_000))))
	token, err := c.app.TokenKeeper.CreateToken(ctx, &tokentypes.MsgCreateToken{
		Creator: creator.String(), Subdenom: "gold", Name: "Gold", Symbol: "GLD", Mint: true, TransferFeeBps: 100,
	})
	require.NoError(t, err)
	require.NoError(t, c.app.TokenKeeper.Mint(ctx, &tokentypes.MsgMint{
		Sender: creator.String(), Denom: token.Denom, Recipient: holder.String(), Amount: math.NewInt(10_000),
	}))
	return &tokenTransferFixture{chain: c, ctx: ctx, denom: token.Denom, holder: holder, other: other}
}

func (f *tokenTransferFixture) transfer(to sdk.AccAddress, amount int64) error {
	return f.chain.app.TokenKeeper.Transfer(f.ctx, &tokentypes.MsgTransfer{
		Sender: f.holder.String(), From: f.holder.String(), To: to.String(), Denom: f.denom, Amount: math.NewInt(amount),
	})
}

func (f *tokenTransferFixture) balance(a sdk.AccAddress) math.Int {
	return f.chain.app.BankKeeper.GetBalance(f.ctx, a, f.denom).Amount
}

// With the real bank keeper: the net amount is sent first and the fee taken second. When the second step
// fails, the first send must be undone too, or the receiver would be paid by a transfer that failed.
func TestTokenTransfer_realBank_failedSettleRollsBackTheSendThatAlreadySucceeded(t *testing.T) {
	f := newTokenTransferFixture(t)
	tokenModule := authtypes.NewModuleAddress(tokentypes.ModuleName)

	// The control: the transfer works when nothing fails, on an identical holder balance.
	require.NoError(t, f.transfer(f.other, 1_000))
	require.Equal(t, math.NewInt(990), f.balance(f.other))
	require.Equal(t, math.NewInt(9_000), f.balance(f.holder))

	netSendSeen := false
	injected := errors.New("injected failure of the fee step")
	f.chain.app.BankKeeper.AppendSendRestriction(func(_ context.Context, _, to sdk.AccAddress, amt sdk.Coins) (sdk.AccAddress, error) {
		if amt.AmountOf(f.denom).IsZero() {
			return to, nil
		}
		if to.Equals(tokenModule) {
			return nil, injected
		}
		netSendSeen = true
		return to, nil
	})

	third := sdk.AccAddress([]byte("token_transfer_third"))
	err := f.transfer(third, 1_000)
	require.ErrorIs(t, err, injected)
	require.True(t, netSendSeen, "the net send must have succeeded before the fee step failed, or this test proves nothing")
	require.True(t, f.balance(third).IsZero(), "the receiver kept coins from a transfer that failed")
	require.Equal(t, math.NewInt(9_000), f.balance(f.holder), "the sender lost coins in a transfer that failed")
}

// A module account is on x/bank's blocked list, which MsgSend enforces; the keeper's own bank.SendCoins
// does not, so MsgTransfer has to.
func TestTokenTransfer_realBank_refusesAModuleAccountAsReceiver(t *testing.T) {
	f := newTokenTransferFixture(t)
	for _, module := range []string{tokentypes.ModuleName, "fee_collector", "bonded_tokens_pool"} {
		err := f.transfer(authtypes.NewModuleAddress(module), 1_000)
		require.ErrorContains(t, err, "module account", module)
		require.Equal(t, math.NewInt(10_000), f.balance(f.holder), "%s: the sender's balance moved", module)
	}
}
