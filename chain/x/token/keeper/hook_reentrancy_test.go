package keeper_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/token/types"
)

// A hook is a contract call, so it can do what a contract can: mint the token it created and burn
// its own coins, in the same transfer. The transfer must act on the state the hook left, not on the
// state it read before calling the hook. These tests drive the hook the way wasmbindings does:
// through the keeper, with the hook's context.

func hookedFeeToken(t *testing.T, f *testFixture) types.Token {
	t.Helper()
	return f.create(t, hookContract, "loop", func(m *types.MsgCreateToken) {
		m.Mint = true
		m.TransferFeeBps = 100
		m.TransferHook = hookContract.String()
	})
}

func requireIssuedEqualsSupply(t *testing.T, f *testFixture, denom string) {
	t.Helper()
	stored, err := f.Keeper.Tokens.Get(f.Ctx, denom)
	require.NoError(t, err)
	require.True(t, stored.Issued.Equal(f.Bank.supplyOf(denom)), "issued %s must equal bank supply %s", stored.Issued, f.Bank.supplyOf(denom))
	f.requireInvariants(t)
}

func TestTransferHook_reentrantMintKeepsIssuedEqualToSupplyAfterTheFeeBurn(t *testing.T) {
	holder, other := addr(2), addr(3)
	var f *testFixture
	f = newTestFixture(t, hookFunc(func(ctx context.Context, _ sdk.AccAddress, denom string, _, _ sdk.AccAddress, _ math.Int) error {
		return f.Keeper.Mint(sdk.UnwrapSDKContext(ctx), &types.MsgMint{Sender: hookContract.String(), Denom: denom, Recipient: hookContract.String(), Amount: math.NewInt(7)})
	}))
	f.initGenesis(t, nil)
	token := hookedFeeToken(t, f)
	f.mint(t, hookContract, token.Denom, holder, 10_000)

	require.NoError(t, f.Keeper.Transfer(f.Ctx, transferMsg(holder, other, token.Denom, 10_000)))
	require.True(t, f.Bank.supplyOf(token.Denom).Equal(math.NewInt(10_000-100+7)))
	requireIssuedEqualsSupply(t, f, token.Denom)
}

func TestTransferHook_reentrantBurnKeepsIssuedEqualToSupplyAfterTheFeeBurn(t *testing.T) {
	holder, other := addr(2), addr(3)
	var f *testFixture
	f = newTestFixture(t, hookFunc(func(ctx context.Context, _ sdk.AccAddress, denom string, _, _ sdk.AccAddress, _ math.Int) error {
		return f.Keeper.Burn(sdk.UnwrapSDKContext(ctx), &types.MsgBurn{Sender: hookContract.String(), Denom: denom, Amount: math.NewInt(5)})
	}))
	f.initGenesis(t, nil)
	token := hookedFeeToken(t, f)
	f.mint(t, hookContract, token.Denom, hookContract, 50)
	f.mint(t, hookContract, token.Denom, holder, 10_000)

	require.NoError(t, f.Keeper.Transfer(f.Ctx, transferMsg(holder, other, token.Denom, 10_000)))
	requireIssuedEqualsSupply(t, f, token.Denom)
}

// A hook that spends the coins the transfer is about to move cannot make the transfer succeed on
// the balance read before the hook ran: the balance is checked again after the hook.
func TestTransferHook_reentrantBurnOfTheSendersCoinsFailsTheTransfer(t *testing.T) {
	other := addr(3)
	var f *testFixture
	f = newTestFixture(t, hookFunc(func(ctx context.Context, _ sdk.AccAddress, denom string, _, _ sdk.AccAddress, _ math.Int) error {
		return f.Keeper.Burn(sdk.UnwrapSDKContext(ctx), &types.MsgBurn{Sender: hookContract.String(), Denom: denom, Amount: math.NewInt(60)})
	}))
	f.initGenesis(t, nil)
	token := hookedFeeToken(t, f)
	f.mint(t, hookContract, token.Denom, hookContract, 100)

	err := f.Keeper.Transfer(f.Ctx, transferMsg(hookContract, other, token.Denom, 100))
	require.ErrorContains(t, err, "insufficient")
	require.True(t, f.Bank.balanceOf(other.String(), token.Denom).IsZero())
}

// A hook that moves the sender's coins with a nested transfer leaves too little for the outer one.
func TestTransferHook_reentrantTransferCannotDoubleSpend(t *testing.T) {
	holder, other := addr(2), addr(3)
	var f *testFixture
	nested := false
	f = newTestFixture(t, hookFunc(func(ctx context.Context, _ sdk.AccAddress, denom string, from, to sdk.AccAddress, _ math.Int) error {
		if nested {
			return nil
		}
		nested = true
		return f.Keeper.Transfer(sdk.UnwrapSDKContext(ctx), transferMsg(from, addr(4), denom, 600))
	}))
	f.initGenesis(t, nil)
	token := hookedFeeToken(t, f)
	f.mint(t, hookContract, token.Denom, holder, 1_000)

	err := f.Keeper.Transfer(f.Ctx, transferMsg(holder, other, token.Denom, 1_000))
	require.ErrorContains(t, err, "insufficient")
	require.True(t, f.Bank.balanceOf(other.String(), token.Denom).IsZero(), "the outer transfer must not pay out")
}

func TestTransferHook_noHookTransferIsUnchanged(t *testing.T) {
	holder, other := addr(2), addr(3)
	f := newTestFixture(t, nil)
	f.initGenesis(t, nil)
	token := f.create(t, addr(1), "plain", func(m *types.MsgCreateToken) { m.Mint = true; m.TransferFeeBps = 100 })
	f.mint(t, addr(1), token.Denom, holder, 10_000)

	require.NoError(t, f.Keeper.Transfer(f.Ctx, transferMsg(holder, other, token.Denom, 10_000)))
	require.True(t, f.Bank.balanceOf(other.String(), token.Denom).Equal(math.NewInt(9_900)))
	requireIssuedEqualsSupply(t, f, token.Denom)
}
