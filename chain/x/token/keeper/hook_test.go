package keeper_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/token/types"
)

func transferMsg(from, to sdk.AccAddress, denom string, amount int64) *types.MsgTransfer {
	return &types.MsgTransfer{Sender: from.String(), From: from.String(), To: to.String(), Denom: denom, Amount: math.NewInt(amount)}
}

func TestTransferHook_allowedTransferRunsTheNamedContractWithTheTransfer(t *testing.T) {
	creator, holder, other := addr(1), addr(2), addr(3)
	var gotContract, gotFrom, gotTo sdk.AccAddress
	var gotDenom string
	var gotAmount math.Int
	f := newTestFixture(t, hookFunc(func(_ context.Context, contract sdk.AccAddress, denom string, from, to sdk.AccAddress, amount math.Int) error {
		gotContract, gotDenom, gotFrom, gotTo, gotAmount = contract, denom, from, to, amount
		return nil
	}))
	f.initGenesis(t, nil)
	token := f.create(t, creator, "cash", func(m *types.MsgCreateToken) { m.Mint = true; m.TransferHook = hookContract.String() })
	f.mint(t, creator, token.Denom, holder, 10)

	require.NoError(t, f.Keeper.Transfer(f.Ctx, transferMsg(holder, other, token.Denom, 4)))
	require.Equal(t, hookContract, gotContract)
	require.Equal(t, token.Denom, gotDenom)
	require.Equal(t, holder, gotFrom)
	require.Equal(t, other, gotTo)
	require.True(t, gotAmount.Equal(math.NewInt(4)))
	require.True(t, f.Bank.balanceOf(other.String(), token.Denom).Equal(math.NewInt(4)))
	f.requireInvariants(t)
}

func TestTransferHook_refusingContractMovesNothing(t *testing.T) {
	creator, holder, other := addr(1), addr(2), addr(3)
	f := newTestFixture(t, hookFunc(func(_ context.Context, _ sdk.AccAddress, _ string, _, _ sdk.AccAddress, amount math.Int) error {
		if amount.Equal(math.NewInt(13)) {
			return errors.New("unlucky")
		}
		return nil
	}))
	f.initGenesis(t, nil)
	token := f.create(t, creator, "cash", func(m *types.MsgCreateToken) { m.Mint = true; m.TransferHook = hookContract.String() })
	f.mint(t, creator, token.Denom, holder, 20)

	err := f.Keeper.Transfer(f.Ctx, transferMsg(holder, other, token.Denom, 13))
	require.ErrorContains(t, err, "transfer hook rejected")
	require.ErrorContains(t, err, "unlucky")
	require.True(t, f.Bank.balanceOf(holder.String(), token.Denom).Equal(math.NewInt(20)))
	require.True(t, f.Bank.balanceOf(other.String(), token.Denom).IsZero())

	require.NoError(t, f.Keeper.Transfer(f.Ctx, transferMsg(holder, other, token.Denom, 5)), "an amount the contract allows still moves")
}

func TestTransferHook_aHookWritesOnlyWhenTheTransferSucceeds(t *testing.T) {
	creator, holder, other := addr(1), addr(2), addr(3)
	var f *testFixture
	f = newTestFixture(t, hookFunc(func(ctx context.Context, _ sdk.AccAddress, denom string, _, _ sdk.AccAddress, _ math.Int) error {
		return f.Keeper.Frozen.Set(ctx, collections.Join(denom, "hook-wrote"))
	}))
	f.initGenesis(t, nil)
	token := f.create(t, creator, "cash", func(m *types.MsgCreateToken) { m.Mint = true; m.TransferHook = hookContract.String() })
	f.mint(t, creator, token.Denom, holder, 1)

	// More than the holder has: the bank send fails after the hook ran, so the hook's write is dropped.
	require.Error(t, f.Keeper.Transfer(f.Ctx, transferMsg(holder, other, token.Denom, 5)))
	frozen, err := f.Keeper.Frozen.Has(f.Ctx, collections.Join(token.Denom, "hook-wrote"))
	require.NoError(t, err)
	require.False(t, frozen, "state a hook wrote must not survive a failed transfer")
}

func TestCreate_aTransferHookNeedsAnExistingContract(t *testing.T) {
	creator := addr(1)
	f := newTestFixture(t, hookFunc(nil))
	f.initGenesis(t, nil)
	f.fund(creator.String(), "norama", math.NewInt(types.CreationFee).MulRaw(2))
	msg := func(hook string) *types.MsgCreateToken {
		return &types.MsgCreateToken{Creator: creator.String(), Subdenom: "cash", Name: "N", Symbol: "S", TransferHook: hook}
	}

	_, err := f.Keeper.CreateToken(f.Ctx, msg(addr(8).String()))
	require.ErrorContains(t, err, "there is no contract at")
	_, err = f.Keeper.CreateToken(f.Ctx, msg("not-an-address"))
	require.ErrorContains(t, err, "invalid transfer hook contract")
	_, err = f.Keeper.CreateToken(f.Ctx, msg(hookContract.String()))
	require.NoError(t, err)
}

func TestCreate_aTransferHookIsRefusedWhenTheBuildHasNoContractVM(t *testing.T) {
	creator := addr(1)
	f := newTestFixture(t, nil)
	f.initGenesis(t, nil)
	f.fund(creator.String(), "norama", math.NewInt(types.CreationFee).MulRaw(2))
	_, err := f.Keeper.CreateToken(f.Ctx, &types.MsgCreateToken{Creator: creator.String(), Subdenom: "cash", Name: "N", Symbol: "S", TransferHook: hookContract.String()})
	require.ErrorContains(t, err, "no contract VM")
}

func TestTransferHook_theContractIsFixedAtCreation(t *testing.T) {
	creator := addr(1)
	f := newTestFixture(t, hookFunc(nil))
	f.initGenesis(t, nil)
	token := f.create(t, creator, "cash", func(m *types.MsgCreateToken) { m.TransferHook = hookContract.String() })
	require.Equal(t, hookContract.String(), token.Extensions.TransferHook)
	require.NoError(t, f.Keeper.Renounce(f.Ctx, &types.MsgRenounce{Sender: creator.String(), Denom: token.Denom, Extension: types.EXTENSION_TRANSFER_HOOK}))
	stored, err := f.Keeper.Tokens.Get(f.Ctx, token.Denom)
	require.NoError(t, err)
	require.Empty(t, stored.Extensions.TransferHook)
	require.Error(t, f.Keeper.Renounce(f.Ctx, &types.MsgRenounce{Sender: creator.String(), Denom: token.Denom, Extension: types.EXTENSION_TRANSFER_HOOK}), "a renounced hook cannot be renounced again, and nothing sets it again")
}
