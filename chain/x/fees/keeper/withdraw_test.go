package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/fees/keeper"
	"github.com/DeBrosOfficial/network/chain/x/fees/types"
)

func TestWithdrawEarnings_movesEarningsToTheOwnersBankBalance(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	addr := sdk.AccAddress("withdrawer_one_______")
	fundedEarnings(t, f, addr, 1_000)

	require.NoError(t, f.Keeper.WithdrawEarnings(f.Ctx, addr, math.NewInt(400)))

	balance, err := f.Keeper.GetEarnings(f.Ctx, addr)
	require.NoError(t, err)
	require.True(t, balance.Equal(math.NewInt(600)), "earnings fall by exactly the amount")
	require.True(t, f.Bank.balanceOf(addr.String()).Equal(math.NewInt(400)), "the owner's bank balance rises by the amount")
	require.True(t, f.Bank.balanceOf(types.ModuleName).Equal(math.NewInt(600)), "the fees module keeps only what is still earnings")
	inv, err := f.Keeper.CheckInvariants(f.Ctx)
	require.NoError(t, err)
	require.True(t, inv.EarningsMatchModule, inv.Detail)
}

func TestWithdrawEarnings_allOfItClearsTheLedgerEntry(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	addr := sdk.AccAddress("withdrawer_two_______")
	fundedEarnings(t, f, addr, 250)

	require.NoError(t, f.Keeper.WithdrawEarnings(f.Ctx, addr, math.NewInt(250)))

	has, err := f.Keeper.Earnings.Has(f.Ctx, addr.String())
	require.NoError(t, err)
	require.False(t, has, "a fully withdrawn balance must remove the key, not store a zero")
	require.True(t, f.Bank.balanceOf(addr.String()).Equal(math.NewInt(250)))
}

func TestWithdrawEarnings_moreThanTheBalanceMovesNothing(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	addr := sdk.AccAddress("withdrawer_three_____")
	fundedEarnings(t, f, addr, 100)

	err := f.Keeper.WithdrawEarnings(f.Ctx, addr, math.NewInt(101))

	require.ErrorContains(t, err, "insufficient earnings")
	balance, gerr := f.Keeper.GetEarnings(f.Ctx, addr)
	require.NoError(t, gerr)
	require.True(t, balance.Equal(math.NewInt(100)))
	require.True(t, f.Bank.balanceOf(addr.String()).IsZero())
}

func TestWithdrawEarnings_noEarningsAtAll(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	err := f.Keeper.WithdrawEarnings(f.Ctx, sdk.AccAddress("nobody_______________"), math.NewInt(1))
	require.ErrorContains(t, err, "insufficient earnings")
}

func TestWithdrawEarnings_refusesANonPositiveAmount(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	addr := sdk.AccAddress("withdrawer_four______")
	fundedEarnings(t, f, addr, 100)

	for name, amount := range map[string]math.Int{"zero": math.ZeroInt(), "negative": math.NewInt(-5), "nil": {}} {
		t.Run(name, func(t *testing.T) {
			require.ErrorContains(t, f.Keeper.WithdrawEarnings(f.Ctx, addr, amount), "must be positive")
		})
	}
	balance, err := f.Keeper.GetEarnings(f.Ctx, addr)
	require.NoError(t, err)
	require.True(t, balance.Equal(math.NewInt(100)))
}

func TestWithdrawEarnings_leavesOtherAccountsAlone(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	alice := sdk.AccAddress("withdrawer_alice_____")
	bob := sdk.AccAddress("withdrawer_bob_______")
	fundedEarnings(t, f, alice, 300)
	fundedEarnings(t, f, bob, 700)

	require.NoError(t, f.Keeper.WithdrawEarnings(f.Ctx, alice, math.NewInt(300)))

	got, err := f.Keeper.GetEarnings(f.Ctx, bob)
	require.NoError(t, err)
	require.True(t, got.Equal(math.NewInt(700)))
	require.True(t, f.Bank.balanceOf(bob.String()).IsZero())
}

func TestWithdrawEarnings_leavesTheFeeOnlyBalanceAlone(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	operator := sdk.AccAddress("operator_____________")
	hotKey := sdk.AccAddress("hot_key______________")
	fundedEarnings(t, f, operator, 500)
	require.NoError(t, f.Keeper.FundFeeBalance(f.Ctx, operator, hotKey, math.NewInt(200)))

	err := f.Keeper.WithdrawEarnings(f.Ctx, hotKey, math.NewInt(1))

	require.ErrorContains(t, err, "insufficient earnings", "a fee-only balance is not earnings and cannot be withdrawn")
	fee, ferr := f.Keeper.GetFeeBalance(f.Ctx, hotKey)
	require.NoError(t, ferr)
	require.True(t, fee.Equal(math.NewInt(200)))
}

func TestMsgServer_withdrawEarningsEmitsAnEventAndReportsWhatIsLeft(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	addr := sdk.AccAddress("withdrawer_event_____")
	fundedEarnings(t, f, addr, 900)
	server := keeper.NewMsgServerImpl(f.Keeper)

	_, err := server.WithdrawEarnings(f.Ctx, &types.MsgWithdrawEarnings{Signer: addr.String(), Amount: math.NewInt(300)})

	require.NoError(t, err)
	events := f.Ctx.EventManager().Events()
	var found sdk.Event
	for _, e := range events {
		if e.Type == types.EventTypeWithdrawEarnings {
			found = sdk.Event(e)
		}
	}
	require.Equal(t, types.EventTypeWithdrawEarnings, found.Type, "the withdrawal emits its event")
	attrs := map[string]string{}
	for _, a := range found.Attributes {
		attrs[a.Key] = a.Value
	}
	require.Equal(t, addr.String(), attrs[types.AttributeSigner])
	require.Equal(t, "300", attrs[types.AttributeAmount])
	require.Equal(t, "600", attrs[types.AttributeRemaining])
}

func TestMsgServer_withdrawEarningsRefusesABadMessage(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	server := keeper.NewMsgServerImpl(f.Keeper)

	_, err := server.WithdrawEarnings(f.Ctx, nil)
	require.Error(t, err)
	_, err = server.WithdrawEarnings(f.Ctx, &types.MsgWithdrawEarnings{Signer: "not-an-address", Amount: math.NewInt(1)})
	require.Error(t, err)
	_, err = server.WithdrawEarnings(f.Ctx, &types.MsgWithdrawEarnings{Signer: sdk.AccAddress("withdrawer_x_________").String(), Amount: math.ZeroInt()})
	require.Error(t, err)
}
