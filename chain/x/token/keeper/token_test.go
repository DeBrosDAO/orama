package keeper_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	feestypes "github.com/DeBrosOfficial/network/chain/x/fees/types"
	"github.com/DeBrosOfficial/network/chain/x/token/keeper"
	"github.com/DeBrosOfficial/network/chain/x/token/types"
)

func (f *testFixture) create(t *testing.T, creator sdk.AccAddress, sub string, mutate func(*types.MsgCreateToken)) types.Token {
	t.Helper()
	f.fund(creator.String(), params.BaseDenom, math.NewInt(types.CreationFee).MulRaw(2))
	msg := &types.MsgCreateToken{
		Creator:  creator.String(),
		Subdenom: sub,
		Name:     "Name",
		Symbol:   "SYM",
	}
	if mutate != nil {
		mutate(msg)
	}
	token, err := f.Keeper.CreateToken(f.Ctx, msg)
	require.NoError(t, err)
	return token
}

func (f *testFixture) mint(t *testing.T, creator sdk.AccAddress, denom string, to sdk.AccAddress, amount int64) {
	t.Helper()
	require.NoError(t, f.Keeper.Mint(f.Ctx, &types.MsgMint{
		Sender:    creator.String(),
		Denom:     denom,
		Recipient: to.String(),
		Amount:    math.NewInt(amount),
	}))
}

func TestCreate_burnsFeeAndLocksDeposit(t *testing.T) {
	f := newTestFixture(t, nil)
	f.initGenesis(t, nil)
	creator := addr(1)
	msg := &types.MsgCreateToken{
		Creator:  creator.String(),
		Subdenom: "cash",
		Name:     "Cash",
		Symbol:   "CASH",
	}
	p := types.DefaultParams()
	deposit := types.DepositFor(p.DepositPerByte, msg.Subdenom, msg.Name, msg.Symbol, msg.Description)
	f.fund(creator.String(), params.BaseDenom, p.CreationFee.Add(deposit))

	res, err := keeper.NewMsgServerImpl(f.Keeper).CreateToken(f.Ctx, msg)
	require.NoError(t, err)
	require.Equal(t, types.Denom(creator.String(), "cash"), res.Denom)

	require.True(t, f.Bank.balanceOf(creator.String(), params.BaseDenom).IsZero())
	require.True(t, f.Bank.balanceOf(types.ModuleName, params.BaseDenom).IsZero(), "the creation fee must be burned, not left in the module")
	require.True(t, f.Bank.burnedOf(params.BaseDenom).Equal(math.NewInt(types.CreationFee)))
	require.True(t, f.Bank.balanceOf(feestypes.DepositsModuleName, params.BaseDenom).Equal(deposit))

	locked, err := f.Fees.GetDeposit(f.Ctx, types.DepositID(res.Denom))
	require.NoError(t, err)
	require.Equal(t, creator.String(), locked.Owner)
	require.True(t, locked.Amount.Equal(deposit))

	token, err := f.Keeper.Tokens.Get(f.Ctx, res.Denom)
	require.NoError(t, err)
	require.True(t, token.Issued.IsZero())
	require.True(t, token.DepositAmount.Equal(deposit))
	require.False(t, token.Shieldable)
	f.requireInvariants(t)

	_, err = f.Keeper.CreateToken(f.Ctx, msg)
	require.Error(t, err, "a second create of the same denom must fail")
	require.True(t, f.Bank.burnedOf(params.BaseDenom).Equal(math.NewInt(types.CreationFee)), "a rejected create must not burn again")
}

func TestCreate_rejectsBadSubdenomAndInsufficientFunds(t *testing.T) {
	f := newTestFixture(t, nil)
	f.initGenesis(t, nil)
	creator := addr(1)
	f.fund(creator.String(), params.BaseDenom, math.NewInt(types.CreationFee).MulRaw(2))

	for _, sub := range []string{"", "A", "ab/c", "1abc", "CASH"} {
		_, err := f.Keeper.CreateToken(f.Ctx, &types.MsgCreateToken{
			Creator: creator.String(), Subdenom: sub, Name: "Name", Symbol: "SYM",
		})
		require.Error(t, err, sub)
	}

	poor := addr(2)
	f.fund(poor.String(), params.BaseDenom, math.NewInt(1))
	_, err := f.Keeper.CreateToken(f.Ctx, &types.MsgCreateToken{
		Creator: poor.String(), Subdenom: "cash", Name: "Name", Symbol: "SYM",
	})
	require.Error(t, err)
	require.True(t, f.Bank.burnedOf(params.BaseDenom).IsZero())
	_, err = f.Keeper.Tokens.Get(f.Ctx, types.Denom(poor.String(), "cash"))
	require.Error(t, err)
}

func TestCreationFee_isTenOramaNotADollarPrice(t *testing.T) {
	require.Equal(t, int64(10_000_000_000), types.CreationFee)
	require.Equal(t, int64(10)*int64(params.NoramaPerOrama), types.CreationFee)
	require.Equal(t, int64(68359), types.DepositPerByte)
	require.True(t, types.DefaultParams().CreationFee.Equal(math.NewInt(types.CreationFee)))
	require.NoError(t, types.DefaultParams().Validate())
}

func TestMintBurnAndTransfer(t *testing.T) {
	f := newTestFixture(t, nil)
	f.initGenesis(t, nil)
	creator := addr(1)
	recipient := addr(2)
	token := f.create(t, creator, "cash", func(m *types.MsgCreateToken) { m.Mint = true })

	f.mint(t, creator, token.Denom, creator, 1_000)
	require.True(t, f.Bank.balanceOf(creator.String(), token.Denom).Equal(math.NewInt(1_000)))
	require.True(t, f.Bank.supplyOf(token.Denom).Equal(math.NewInt(1_000)))
	f.requireInvariants(t)

	require.NoError(t, f.Keeper.Transfer(f.Ctx, &types.MsgTransfer{
		Sender: creator.String(),
		From:   creator.String(),
		To:     recipient.String(),
		Denom:  token.Denom,
		Amount: math.NewInt(400),
	}))
	require.True(t, f.Bank.balanceOf(creator.String(), token.Denom).Equal(math.NewInt(600)))
	require.True(t, f.Bank.balanceOf(recipient.String(), token.Denom).Equal(math.NewInt(400)))
	require.True(t, f.Bank.supplyOf(token.Denom).Equal(math.NewInt(1_000)), "a fee-less transfer must not change supply")

	require.NoError(t, f.Keeper.Burn(f.Ctx, &types.MsgBurn{
		Sender: recipient.String(),
		Denom:  token.Denom,
		Amount: math.NewInt(150),
	}))
	require.True(t, f.Bank.balanceOf(recipient.String(), token.Denom).Equal(math.NewInt(250)))
	require.True(t, f.Bank.burnedOf(token.Denom).Equal(math.NewInt(150)))
	require.True(t, f.Bank.supplyOf(token.Denom).Equal(math.NewInt(850)))
	stored, err := f.Keeper.Tokens.Get(f.Ctx, token.Denom)
	require.NoError(t, err)
	require.True(t, stored.Issued.Equal(math.NewInt(850)))
	f.requireInvariants(t)

	require.Error(t, f.Keeper.Burn(f.Ctx, &types.MsgBurn{
		Sender: creator.String(), Denom: token.Denom, Amount: math.NewInt(10_000),
	}))
	require.Error(t, f.Keeper.Mint(f.Ctx, &types.MsgMint{
		Sender: recipient.String(), Denom: token.Denom, Recipient: recipient.String(), Amount: math.NewInt(1),
	}), "only the creator holding mint authority may mint")
}

func TestExtensions_eachCanBeUsedAndOnlyRenounced(t *testing.T) {
	creator := addr(1)
	holder := addr(2)
	delegate := addr(3)
	other := addr(4)

	t.Run("mint", func(t *testing.T) {
		f := newTestFixture(t, nil)
		f.initGenesis(t, nil)
		token := f.create(t, creator, "cash", func(m *types.MsgCreateToken) { m.Mint = true })
		f.mint(t, creator, token.Denom, holder, 10)
		require.NoError(t, f.Keeper.Renounce(f.Ctx, &types.MsgRenounce{
			Sender: creator.String(), Denom: token.Denom, Extension: types.EXTENSION_MINT,
		}))
		require.Error(t, f.Keeper.Mint(f.Ctx, &types.MsgMint{
			Sender: creator.String(), Denom: token.Denom, Recipient: holder.String(), Amount: math.NewInt(1),
		}))
		require.Error(t, f.Keeper.Renounce(f.Ctx, &types.MsgRenounce{
			Sender: creator.String(), Denom: token.Denom, Extension: types.EXTENSION_MINT,
		}))
	})

	t.Run("freeze", func(t *testing.T) {
		f := newTestFixture(t, nil)
		f.initGenesis(t, nil)
		token := f.create(t, creator, "cash", func(m *types.MsgCreateToken) {
			m.Mint = true
			m.Freeze = true
		})
		f.mint(t, creator, token.Denom, holder, 10)
		require.NoError(t, f.Keeper.SetFrozen(f.Ctx, &types.MsgSetFrozen{
			Sender: creator.String(), Denom: token.Denom, Account: holder.String(), Frozen: true,
		}))
		require.Error(t, f.Keeper.Transfer(f.Ctx, &types.MsgTransfer{
			Sender: holder.String(), From: holder.String(), To: other.String(), Denom: token.Denom, Amount: math.NewInt(1),
		}))
		require.Error(t, f.Keeper.Burn(f.Ctx, &types.MsgBurn{
			Sender: holder.String(), Denom: token.Denom, Amount: math.NewInt(1),
		}))
		require.NoError(t, f.Keeper.SetFrozen(f.Ctx, &types.MsgSetFrozen{
			Sender: creator.String(), Denom: token.Denom, Account: holder.String(), Frozen: false,
		}))
		require.NoError(t, f.Keeper.Transfer(f.Ctx, &types.MsgTransfer{
			Sender: holder.String(), From: holder.String(), To: other.String(), Denom: token.Denom, Amount: math.NewInt(1),
		}))
		require.NoError(t, f.Keeper.SetFrozen(f.Ctx, &types.MsgSetFrozen{
			Sender: creator.String(), Denom: token.Denom, Account: holder.String(), Frozen: true,
		}))
		require.NoError(t, f.Keeper.Renounce(f.Ctx, &types.MsgRenounce{
			Sender: creator.String(), Denom: token.Denom, Extension: types.EXTENSION_FREEZE,
		}))
		require.Error(t, f.Keeper.SetFrozen(f.Ctx, &types.MsgSetFrozen{
			Sender: creator.String(), Denom: token.Denom, Account: other.String(), Frozen: true,
		}))
		require.Error(t, f.Keeper.Transfer(f.Ctx, &types.MsgTransfer{
			Sender: holder.String(), From: holder.String(), To: other.String(), Denom: token.Denom, Amount: math.NewInt(1),
		}), "renouncing freeze must not clear an existing freeze")
	})

	t.Run("permanent delegate", func(t *testing.T) {
		f := newTestFixture(t, nil)
		f.initGenesis(t, nil)
		token := f.create(t, creator, "cash", func(m *types.MsgCreateToken) {
			m.Mint = true
			m.PermanentDelegate = delegate.String()
		})
		f.mint(t, creator, token.Denom, holder, 10)
		require.Error(t, f.Keeper.Transfer(f.Ctx, &types.MsgTransfer{
			Sender: other.String(), From: holder.String(), To: other.String(), Denom: token.Denom, Amount: math.NewInt(4),
		}))
		require.NoError(t, f.Keeper.Transfer(f.Ctx, &types.MsgTransfer{
			Sender: delegate.String(), From: holder.String(), To: other.String(), Denom: token.Denom, Amount: math.NewInt(4),
		}))
		require.True(t, f.Bank.balanceOf(holder.String(), token.Denom).Equal(math.NewInt(6)))
		require.True(t, f.Bank.balanceOf(other.String(), token.Denom).Equal(math.NewInt(4)))
		require.Error(t, f.Keeper.Renounce(f.Ctx, &types.MsgRenounce{
			Sender: creator.String(), Denom: token.Denom, Extension: types.EXTENSION_PERMANENT_DELEGATE,
		}), "only the delegate can renounce that power")
		require.NoError(t, f.Keeper.Renounce(f.Ctx, &types.MsgRenounce{
			Sender: delegate.String(), Denom: token.Denom, Extension: types.EXTENSION_PERMANENT_DELEGATE,
		}))
		require.Error(t, f.Keeper.Transfer(f.Ctx, &types.MsgTransfer{
			Sender: delegate.String(), From: holder.String(), To: other.String(), Denom: token.Denom, Amount: math.NewInt(1),
		}))
	})

	t.Run("transfer fee", func(t *testing.T) {
		f := newTestFixture(t, nil)
		f.initGenesis(t, nil)
		token := f.create(t, creator, "cash", func(m *types.MsgCreateToken) {
			m.Mint = true
			m.TransferFeeBps = 100
		})
		f.mint(t, creator, token.Denom, holder, 10_000)
		require.NoError(t, f.Keeper.Transfer(f.Ctx, &types.MsgTransfer{
			Sender: holder.String(), From: holder.String(), To: other.String(), Denom: token.Denom, Amount: math.NewInt(10_000),
		}))
		require.True(t, f.Bank.balanceOf(other.String(), token.Denom).Equal(math.NewInt(9_900)))
		require.True(t, f.Bank.balanceOf(holder.String(), token.Denom).IsZero())
		require.True(t, f.Bank.burnedOf(token.Denom).Equal(math.NewInt(100)))
		require.True(t, f.Bank.supplyOf(token.Denom).Equal(math.NewInt(9_900)))
		f.requireInvariants(t)
		require.NoError(t, f.Keeper.Renounce(f.Ctx, &types.MsgRenounce{
			Sender: creator.String(), Denom: token.Denom, Extension: types.EXTENSION_TRANSFER_FEE,
		}))
		f.mint(t, creator, token.Denom, holder, 100)
		require.NoError(t, f.Keeper.Transfer(f.Ctx, &types.MsgTransfer{
			Sender: holder.String(), From: holder.String(), To: creator.String(), Denom: token.Denom, Amount: math.NewInt(100),
		}))
		require.True(t, f.Bank.burnedOf(token.Denom).Equal(math.NewInt(100)), "renounced fee must not burn more")
	})

	t.Run("non-transferable", func(t *testing.T) {
		f := newTestFixture(t, nil)
		f.initGenesis(t, nil)
		token := f.create(t, creator, "cash", func(m *types.MsgCreateToken) {
			m.Mint = true
			m.NonTransferable = true
			m.PermanentDelegate = delegate.String()
		})
		f.mint(t, creator, token.Denom, holder, 5)
		require.Error(t, f.Keeper.Transfer(f.Ctx, &types.MsgTransfer{
			Sender: holder.String(), From: holder.String(), To: other.String(), Denom: token.Denom, Amount: math.NewInt(1),
		}))
		require.Error(t, f.Keeper.Transfer(f.Ctx, &types.MsgTransfer{
			Sender: delegate.String(), From: holder.String(), To: other.String(), Denom: token.Denom, Amount: math.NewInt(1),
		}), "non-transferable blocks the permanent delegate too")
		require.NoError(t, f.Keeper.Renounce(f.Ctx, &types.MsgRenounce{
			Sender: creator.String(), Denom: token.Denom, Extension: types.EXTENSION_NON_TRANSFERABLE,
		}))
		require.NoError(t, f.Keeper.Transfer(f.Ctx, &types.MsgTransfer{
			Sender: delegate.String(), From: holder.String(), To: other.String(), Denom: token.Denom, Amount: math.NewInt(1),
		}))
	})

	t.Run("pause", func(t *testing.T) {
		f := newTestFixture(t, nil)
		f.initGenesis(t, nil)
		token := f.create(t, creator, "cash", func(m *types.MsgCreateToken) {
			m.Mint = true
			m.Pause = true
		})
		f.mint(t, creator, token.Denom, holder, 5)
		require.NoError(t, f.Keeper.SetPaused(f.Ctx, &types.MsgSetPaused{
			Sender: creator.String(), Denom: token.Denom, Paused: true,
		}))
		require.Error(t, f.Keeper.Transfer(f.Ctx, &types.MsgTransfer{
			Sender: holder.String(), From: holder.String(), To: other.String(), Denom: token.Denom, Amount: math.NewInt(1),
		}))
		f.mint(t, creator, token.Denom, holder, 1)
		require.NoError(t, f.Keeper.SetPaused(f.Ctx, &types.MsgSetPaused{
			Sender: creator.String(), Denom: token.Denom, Paused: false,
		}))
		require.NoError(t, f.Keeper.Transfer(f.Ctx, &types.MsgTransfer{
			Sender: holder.String(), From: holder.String(), To: other.String(), Denom: token.Denom, Amount: math.NewInt(1),
		}))
		require.NoError(t, f.Keeper.SetPaused(f.Ctx, &types.MsgSetPaused{
			Sender: creator.String(), Denom: token.Denom, Paused: true,
		}))
		require.NoError(t, f.Keeper.Renounce(f.Ctx, &types.MsgRenounce{
			Sender: creator.String(), Denom: token.Denom, Extension: types.EXTENSION_PAUSE,
		}))
		require.Error(t, f.Keeper.SetPaused(f.Ctx, &types.MsgSetPaused{
			Sender: creator.String(), Denom: token.Denom, Paused: false,
		}), "renouncing pause must not clear an already-paused token")
		require.Error(t, f.Keeper.Transfer(f.Ctx, &types.MsgTransfer{
			Sender: holder.String(), From: holder.String(), To: other.String(), Denom: token.Denom, Amount: math.NewInt(1),
		}))
	})

	t.Run("transfer hook", func(t *testing.T) {
		var calls int
		f := newTestFixture(t, hookFunc(func(ctx context.Context, _ string, _, _ sdk.AccAddress, _ math.Int) error {
			calls++
			sdk.UnwrapSDKContext(ctx).GasMeter().ConsumeGas(types.TransferHookGasCap, "within cap")
			return nil
		}))
		f.initGenesis(t, nil)
		token := f.create(t, creator, "cash", func(m *types.MsgCreateToken) {
			m.Mint = true
			m.TransferHook = true
		})
		f.mint(t, creator, token.Denom, holder, 3)
		require.NoError(t, f.Keeper.Transfer(f.Ctx, &types.MsgTransfer{
			Sender: holder.String(), From: holder.String(), To: other.String(), Denom: token.Denom, Amount: math.NewInt(1),
		}))
		require.Equal(t, 1, calls)
		require.NoError(t, f.Keeper.Renounce(f.Ctx, &types.MsgRenounce{
			Sender: creator.String(), Denom: token.Denom, Extension: types.EXTENSION_TRANSFER_HOOK,
		}))
		require.NoError(t, f.Keeper.Transfer(f.Ctx, &types.MsgTransfer{
			Sender: holder.String(), From: holder.String(), To: other.String(), Denom: token.Denom, Amount: math.NewInt(1),
		}))
		require.Equal(t, 1, calls, "a renounced hook must not be called")
	})
}

func TestTransferHook_gasCapFailsTheTransfer(t *testing.T) {
	f := newTestFixture(t, hookFunc(func(ctx context.Context, _ string, _, _ sdk.AccAddress, _ math.Int) error {
		sdk.UnwrapSDKContext(ctx).GasMeter().ConsumeGas(types.TransferHookGasCap+1, "over cap")
		return nil
	}))
	f.initGenesis(t, nil)
	creator := addr(1)
	holder := addr(2)
	other := addr(3)
	token := f.create(t, creator, "cash", func(m *types.MsgCreateToken) {
		m.Mint = true
		m.TransferHook = true
	})
	f.mint(t, creator, token.Denom, holder, 8)
	before := f.Ctx.GasMeter().GasConsumed()
	err := f.Keeper.Transfer(f.Ctx, &types.MsgTransfer{
		Sender: holder.String(), From: holder.String(), To: other.String(), Denom: token.Denom, Amount: math.NewInt(5),
	})
	require.ErrorIs(t, err, types.ErrHookGasCap)
	require.GreaterOrEqual(t, f.Ctx.GasMeter().GasConsumed()-before, types.TransferHookGasCap)
	require.True(t, f.Bank.balanceOf(holder.String(), token.Denom).Equal(math.NewInt(8)))
	require.True(t, f.Bank.balanceOf(other.String(), token.Denom).IsZero())
	require.True(t, f.Bank.supplyOf(token.Denom).Equal(math.NewInt(8)))
	f.requireInvariants(t)
}

func TestSetShieldable_refusesFrozenCapableToken(t *testing.T) {
	creator := addr(1)
	delegate := addr(2)
	stranger := addr(3)

	cases := []struct {
		name   string
		mutate func(*types.MsgCreateToken)
		ext    types.Extension
		signer sdk.AccAddress
	}{
		{name: "freeze", mutate: func(m *types.MsgCreateToken) { m.Freeze = true }, ext: types.EXTENSION_FREEZE, signer: creator},
		{name: "pause", mutate: func(m *types.MsgCreateToken) { m.Pause = true }, ext: types.EXTENSION_PAUSE, signer: creator},
		{
			name:   "permanent delegate",
			mutate: func(m *types.MsgCreateToken) { m.PermanentDelegate = delegate.String() },
			ext:    types.EXTENSION_PERMANENT_DELEGATE,
			signer: delegate,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newTestFixture(t, nil)
			f.initGenesis(t, nil)
			token := f.create(t, creator, "cash", tc.mutate)
			err := f.Keeper.SetShieldable(f.Ctx, &types.MsgSetShieldable{Sender: stranger.String(), Denom: token.Denom})
			require.ErrorIs(t, err, types.ErrShieldPowers)
			stored, getErr := f.Keeper.Tokens.Get(f.Ctx, token.Denom)
			require.NoError(t, getErr)
			require.False(t, stored.Shieldable)

			require.NoError(t, f.Keeper.Renounce(f.Ctx, &types.MsgRenounce{
				Sender: tc.signer.String(), Denom: token.Denom, Extension: tc.ext,
			}))
			require.NoError(t, f.Keeper.SetShieldable(f.Ctx, &types.MsgSetShieldable{Sender: stranger.String(), Denom: token.Denom}))
			stored, getErr = f.Keeper.Tokens.Get(f.Ctx, token.Denom)
			require.NoError(t, getErr)
			require.True(t, stored.Shieldable)
		})
	}

	t.Run("one remaining power still refuses", func(t *testing.T) {
		f := newTestFixture(t, nil)
		f.initGenesis(t, nil)
		token := f.create(t, creator, "cash", func(m *types.MsgCreateToken) {
			m.Freeze = true
			m.Pause = true
		})
		require.NoError(t, f.Keeper.Renounce(f.Ctx, &types.MsgRenounce{
			Sender: creator.String(), Denom: token.Denom, Extension: types.EXTENSION_FREEZE,
		}))
		err := f.Keeper.SetShieldable(f.Ctx, &types.MsgSetShieldable{Sender: creator.String(), Denom: token.Denom})
		require.ErrorIs(t, err, types.ErrShieldPowers)
	})

	t.Run("a token without those powers can be marked and still transfers", func(t *testing.T) {
		f := newTestFixture(t, nil)
		f.initGenesis(t, nil)
		token := f.create(t, creator, "cash", func(m *types.MsgCreateToken) { m.Mint = true })
		require.NoError(t, f.Keeper.SetShieldable(f.Ctx, &types.MsgSetShieldable{Sender: stranger.String(), Denom: token.Denom}))
		f.mint(t, creator, token.Denom, creator, 2)
		require.NoError(t, f.Keeper.Transfer(f.Ctx, &types.MsgTransfer{
			Sender: creator.String(), From: creator.String(), To: stranger.String(), Denom: token.Denom, Amount: math.NewInt(2),
		}))
		require.True(t, f.Bank.balanceOf(stranger.String(), token.Denom).Equal(math.NewInt(2)))
	})
}

func TestDeleteToken_refundsDeposit(t *testing.T) {
	f := newTestFixture(t, nil)
	f.initGenesis(t, func(gs *types.GenesisState) {
		gs.Params.DepositPerByte = math.NewInt(1_000)
	})
	creator := addr(1)
	msg := &types.MsgCreateToken{Creator: creator.String(), Subdenom: "ab", Name: "N", Symbol: "S"}
	deposit := math.NewInt(4_000) // 4 metadata bytes * 1000
	f.fund(creator.String(), params.BaseDenom, math.NewInt(types.CreationFee).Add(deposit))
	token, err := f.Keeper.CreateToken(f.Ctx, msg)
	require.NoError(t, err)
	require.True(t, token.DepositAmount.Equal(deposit))

	_, _, err = f.Keeper.DeleteToken(f.Ctx, &types.MsgDeleteToken{Sender: addr(2).String(), Denom: token.Denom})
	require.Error(t, err)

	f.fund(creator.String(), token.Denom, math.NewInt(1))
	require.NoError(t, f.Keeper.Tokens.Set(f.Ctx, token.Denom, func() types.Token {
		token.Issued = math.NewInt(1)
		return token
	}()))
	_, _, err = f.Keeper.DeleteToken(f.Ctx, &types.MsgDeleteToken{Sender: creator.String(), Denom: token.Denom})
	require.Error(t, err, "outstanding supply must keep the deposit locked")
	_, err = f.Fees.GetDeposit(f.Ctx, types.DepositID(token.Denom))
	require.NoError(t, err)

	token.Issued = math.ZeroInt()
	require.NoError(t, f.Keeper.Tokens.Set(f.Ctx, token.Denom, token))
	f.Bank.supply[token.Denom] = math.ZeroInt()
	delete(f.Bank.balances[creator.String()], token.Denom)

	refund, burn, err := f.Keeper.DeleteToken(f.Ctx, &types.MsgDeleteToken{Sender: creator.String(), Denom: token.Denom})
	require.NoError(t, err)
	wantRefund, wantBurn := feestypes.SplitDeposit(deposit, feestypes.DefaultParams())
	require.True(t, refund.Equal(math.NewInt(3_960)), refund.String())
	require.True(t, burn.Equal(math.NewInt(40)), burn.String())
	require.True(t, refund.Equal(wantRefund))
	require.True(t, burn.Equal(wantBurn))
	require.True(t, refund.Add(burn).Equal(deposit))
	require.True(t, f.Fees.earningsOf(creator.String()).Equal(refund), "99% is refunded to earnings, not to the spendable bank balance")
	require.True(t, f.Bank.balanceOf(creator.String(), params.BaseDenom).IsZero())
	require.True(t, f.Fees.burned.Equal(burn))
	_, err = f.Fees.GetDeposit(f.Ctx, types.DepositID(token.Denom))
	require.Error(t, err)
	_, err = f.Keeper.Tokens.Get(f.Ctx, token.Denom)
	require.Error(t, err)
	f.requireInvariants(t)
}

func TestInvariants_breakWhenRecordsDrift(t *testing.T) {
	f := newTestFixture(t, nil)
	f.initGenesis(t, nil)
	creator := addr(1)
	token := f.create(t, creator, "cash", func(m *types.MsgCreateToken) { m.Mint = true })
	f.mint(t, creator, token.Denom, creator, 20)
	f.requireInvariants(t)

	token.Issued = math.NewInt(19)
	require.NoError(t, f.Keeper.Tokens.Set(f.Ctx, token.Denom, token))
	got, err := f.Keeper.CheckInvariants(f.Ctx)
	require.NoError(t, err)
	require.False(t, got.SupplyMatches, got.Detail)
	require.True(t, got.DepositsMatch, got.Detail)

	token.Issued = math.NewInt(20)
	token.DepositAmount = token.DepositAmount.Add(math.NewInt(1))
	require.NoError(t, f.Keeper.Tokens.Set(f.Ctx, token.Denom, token))
	got, err = f.Keeper.CheckInvariants(f.Ctx)
	require.NoError(t, err)
	require.True(t, got.SupplyMatches, got.Detail)
	require.False(t, got.DepositsMatch, got.Detail)
}

func TestGenesis_roundTrip(t *testing.T) {
	f := newTestFixture(t, nil)
	f.initGenesis(t, nil)
	creator := addr(1)
	holder := addr(2)
	token := f.create(t, creator, "cash", func(m *types.MsgCreateToken) {
		m.Mint = true
		m.Freeze = true
	})
	f.mint(t, creator, token.Denom, holder, 7)
	require.NoError(t, f.Keeper.SetFrozen(f.Ctx, &types.MsgSetFrozen{
		Sender: creator.String(), Denom: token.Denom, Account: holder.String(), Frozen: true,
	}))

	exported, err := f.Keeper.ExportGenesis(f.Ctx)
	require.NoError(t, err)
	require.NoError(t, exported.Validate())

	f2 := newTestFixture(t, nil)
	require.NoError(t, f2.Keeper.InitGenesis(f2.Ctx, *exported))
	again, err := f2.Keeper.ExportGenesis(f2.Ctx)
	require.NoError(t, err)
	require.Equal(t, exported.Tokens[0].Denom, again.Tokens[0].Denom)
	require.True(t, exported.Tokens[0].Issued.Equal(again.Tokens[0].Issued))
	require.True(t, exported.Tokens[0].DepositAmount.Equal(again.Tokens[0].DepositAmount))
	require.Equal(t, exported.FrozenAccounts, again.FrozenAccounts)

	q, err := keeper.NewQueryServerImpl(f.Keeper).Invariants(f.Ctx, &types.QueryInvariantsRequest{})
	require.NoError(t, err)
	require.True(t, q.SupplyMatches, q.Detail)
	require.True(t, q.DepositsMatch, q.Detail)
}

func TestCreate_asksForItsExactEarningsTopUpAfterItsChecks(t *testing.T) {
	f := newTestFixture(t, nil)
	f.initGenesis(t, nil)
	creator := addr(1)
	msg := &types.MsgCreateToken{Creator: creator.String(), Subdenom: "cash", Name: "Cash", Symbol: "CASH"}
	p := types.DefaultParams()
	need := p.CreationFee.Add(types.DepositFor(p.DepositPerByte, msg.Subdenom, msg.Name, msg.Symbol, msg.Description))
	f.fund(creator.String(), params.BaseDenom, need)
	srv := keeper.NewMsgServerImpl(f.Keeper)

	_, err := srv.CreateToken(f.Ctx, msg)
	require.NoError(t, err)
	require.Len(t, f.Fees.funded, 1)
	require.True(t, f.Fees.funded[0].addr.Equals(creator))
	require.True(t, f.Fees.funded[0].amount.Equal(need), "the fee plus the metadata deposit")

	_, err = srv.CreateToken(f.Ctx, msg)
	require.Error(t, err, "the denom exists")
	require.Len(t, f.Fees.funded, 1, "a creation its own checks reject never asks for a top-up")
}
