package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/token/types"
)

func tokenCoins(denom string) sdk.Coins { return sdk.NewCoins(sdk.NewCoin(denom, math.NewInt(1))) }

func TestSendRestriction_plainTokenAndOtherDenomsPass(t *testing.T) {
	f := newTestFixture(t, nil)
	f.initGenesis(t, nil)
	creator, a, b := addr(1), addr(2), addr(3)
	token := f.create(t, creator, "cash", nil)

	to, err := f.Keeper.SendRestriction(f.Ctx, a, b, tokenCoins(token.Denom))
	require.NoError(t, err)
	require.Equal(t, b, to)

	_, err = f.Keeper.SendRestriction(f.Ctx, a, b, tokenCoins(params.BaseDenom))
	require.NoError(t, err, "other denoms are not this module's business")

	_, err = f.Keeper.SendRestriction(f.Ctx, a, b, tokenCoins("factory/"+creator.String()+"/unknown"))
	require.NoError(t, err, "a factory-looking denom with no token record is not governed here")
}

func TestSendRestriction_powersHoldOnABankSend(t *testing.T) {
	creator, a, b := addr(1), addr(2), addr(3)
	cases := []struct {
		name   string
		mutate func(*types.MsgCreateToken)
		arm    func(t *testing.T, f *testFixture, denom string)
		want   string
	}{
		{"frozen sender", func(m *types.MsgCreateToken) { m.Freeze = true }, func(t *testing.T, f *testFixture, denom string) {
			require.NoError(t, f.Keeper.SetFrozen(f.Ctx, &types.MsgSetFrozen{Sender: creator.String(), Denom: denom, Account: a.String(), Frozen: true}))
		}, "frozen"},
		{"frozen recipient", func(m *types.MsgCreateToken) { m.Freeze = true }, func(t *testing.T, f *testFixture, denom string) {
			require.NoError(t, f.Keeper.SetFrozen(f.Ctx, &types.MsgSetFrozen{Sender: creator.String(), Denom: denom, Account: b.String(), Frozen: true}))
		}, "frozen"},
		{"paused", func(m *types.MsgCreateToken) { m.Pause = true }, func(t *testing.T, f *testFixture, denom string) {
			require.NoError(t, f.Keeper.SetPaused(f.Ctx, &types.MsgSetPaused{Sender: creator.String(), Denom: denom, Paused: true}))
		}, "paused"},
		{"non-transferable", func(m *types.MsgCreateToken) { m.NonTransferable = true }, nil, "non-transferable"},
		{"transfer fee", func(m *types.MsgCreateToken) { m.TransferFeeBps = 100 }, nil, "MsgTransfer"},
		{"transfer hook", func(m *types.MsgCreateToken) { m.TransferHook = true }, nil, "MsgTransfer"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newTestFixture(t, nil)
			f.initGenesis(t, nil)
			token := f.create(t, creator, "cash", tc.mutate)
			if tc.arm != nil {
				tc.arm(t, f, token.Denom)
			}
			_, err := f.Keeper.SendRestriction(f.Ctx, a, b, tokenCoins(token.Denom))
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestSendRestriction_unfrozenAndUnpausedMoveAgain(t *testing.T) {
	f := newTestFixture(t, nil)
	f.initGenesis(t, nil)
	creator, a, b := addr(1), addr(2), addr(3)
	token := f.create(t, creator, "cash", func(m *types.MsgCreateToken) { m.Freeze = true; m.Pause = true })

	require.NoError(t, f.Keeper.SetFrozen(f.Ctx, &types.MsgSetFrozen{Sender: creator.String(), Denom: token.Denom, Account: a.String(), Frozen: true}))
	_, err := f.Keeper.SendRestriction(f.Ctx, a, b, tokenCoins(token.Denom))
	require.Error(t, err)
	require.NoError(t, f.Keeper.SetFrozen(f.Ctx, &types.MsgSetFrozen{Sender: creator.String(), Denom: token.Denom, Account: a.String(), Frozen: false}))
	_, err = f.Keeper.SendRestriction(f.Ctx, a, b, tokenCoins(token.Denom))
	require.NoError(t, err)
}

func TestSendRestriction_theTokenModuleMovesWhatItMintsAndBurns(t *testing.T) {
	f := newTestFixture(t, nil)
	f.initGenesis(t, nil)
	creator, a := addr(1), addr(2)
	token := f.create(t, creator, "cash", func(m *types.MsgCreateToken) { m.NonTransferable = true })
	module := authtypes.NewModuleAddress(types.ModuleName)

	_, err := f.Keeper.SendRestriction(f.Ctx, module, a, tokenCoins(token.Denom))
	require.NoError(t, err, "mint to a holder of a non-transferable token")
	_, err = f.Keeper.SendRestriction(f.Ctx, a, module, tokenCoins(token.Denom))
	require.NoError(t, err, "burn from a holder")
}
