package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/nodes/types"
)

// An operator with ORAMA in its bank balance and no earnings (a create-network operator runs no
// validator) funds its node's hot key from the bank.
func TestFundHotKey_bankSourceMovesBankBalanceToTheHotKey(t *testing.T) {
	f := newTestFixture(t)
	op, hot := storageNode(t, f, "node-1", []string{"https://93.184.113.10:443"}, 64495)
	bankBefore := f.Bank.balanceOf(op.String())
	require.True(t, f.Earnings.balanceOf(op).IsZero())
	amount := math.NewInt(400)

	_, err := f.Msg.FundHotKey(f.Ctx, &types.MsgFundHotKey{
		Operator: op.String(), NodeId: "node-1", Amount: amount, Source: types.FundSource_FUND_SOURCE_BANK,
	})
	require.NoError(t, err)

	require.True(t, f.Bank.balanceOf(op.String()).Equal(bankBefore.Sub(amount)))
	require.True(t, f.Earnings.feeBalanceOf(hot).Equal(amount), "the hot key gets a fee-only balance")
	require.True(t, f.Earnings.balanceOf(hot).IsZero(), "and no earnings it could bond or shield")
	require.True(t, f.Earnings.balanceOf(op).IsZero(), "the earnings path was not touched")
	require.True(t, f.Accounts.HasAccount(f.Ctx, hot), "the first funding creates the hot key's account")
}

// The bank source never reaches into earnings, even when the operator has them.
func TestFundHotKey_bankSourceLeavesEarningsAlone(t *testing.T) {
	f := newTestFixture(t)
	op, hot := storageNode(t, f, "node-1", nil, 0)
	f.Earnings.balances[op.String()] = math.NewInt(1_000)

	_, err := f.Msg.FundHotKey(f.Ctx, &types.MsgFundHotKey{
		Operator: op.String(), NodeId: "node-1", Amount: math.NewInt(250), Source: types.FundSource_FUND_SOURCE_BANK,
	})
	require.NoError(t, err)
	require.True(t, f.Earnings.balanceOf(op).Equal(math.NewInt(1_000)))
	require.True(t, f.Earnings.feeBalanceOf(hot).Equal(math.NewInt(250)))
}

// An explicit EARNINGS source is the original behaviour.
func TestFundHotKey_earningsSourceIsUnchangedAndLeavesTheBankAlone(t *testing.T) {
	f := newTestFixture(t)
	op, hot := storageNode(t, f, "node-1", nil, 0)
	f.Earnings.balances[op.String()] = math.NewInt(1_000)
	bankBefore := f.Bank.balanceOf(op.String())

	_, err := f.Msg.FundHotKey(f.Ctx, &types.MsgFundHotKey{
		Operator: op.String(), NodeId: "node-1", Amount: math.NewInt(400), Source: types.FundSource_FUND_SOURCE_EARNINGS,
	})
	require.NoError(t, err)
	require.True(t, f.Earnings.balanceOf(op).Equal(math.NewInt(600)))
	require.True(t, f.Earnings.feeBalanceOf(hot).Equal(math.NewInt(400)))
	require.True(t, f.Bank.balanceOf(op.String()).Equal(bankBefore))
}

func TestFundHotKey_bankSourceRefusesMoreThanTheBankBalance(t *testing.T) {
	f := newTestFixture(t)
	op, hot := storageNode(t, f, "node-1", nil, 0)
	f.Earnings.balances[op.String()] = math.NewInt(10_000_000_000_000)
	bankBefore := f.Bank.balanceOf(op.String())

	_, err := f.Msg.FundHotKey(f.Ctx, &types.MsgFundHotKey{
		Operator: op.String(), NodeId: "node-1", Amount: bankBefore.AddRaw(1), Source: types.FundSource_FUND_SOURCE_BANK,
	})
	require.Error(t, err)
	require.True(t, f.Bank.balanceOf(op.String()).Equal(bankBefore), "a refused funding moves nothing")
	require.True(t, f.Earnings.feeBalanceOf(hot).IsZero())
	require.False(t, f.Accounts.HasAccount(f.Ctx, hot), "and creates no account")
}

func TestFundHotKey_refusesAnUnknownSource(t *testing.T) {
	f := newTestFixture(t)
	op, hot := storageNode(t, f, "node-1", nil, 0)
	f.Earnings.balances[op.String()] = math.NewInt(1_000)
	bankBefore := f.Bank.balanceOf(op.String())

	_, err := f.Msg.FundHotKey(f.Ctx, &types.MsgFundHotKey{
		Operator: op.String(), NodeId: "node-1", Amount: math.NewInt(1), Source: types.FundSource(7),
	})
	require.Error(t, err)
	require.True(t, f.Earnings.balanceOf(op).Equal(math.NewInt(1_000)), "an unknown source is not treated as earnings")
	require.True(t, f.Bank.balanceOf(op.String()).Equal(bankBefore))
	require.True(t, f.Earnings.feeBalanceOf(hot).IsZero())
}

// The bank source keeps every rule of the earnings source: the caller owns the node, and a retired
// node has no live hot key.
func TestFundHotKey_bankSourceKeepsTheOwnerAndRetiredRules(t *testing.T) {
	f := newTestFixture(t)
	op, hot := storageNode(t, f, "node-1", nil, 0)
	thief := newAccount(t)
	f.Bank.fund(thief.String(), math.NewInt(5).MulRaw(params.NoramaPerOrama))
	bank := types.FundSource_FUND_SOURCE_BANK

	_, err := f.Msg.FundHotKey(f.Ctx, &types.MsgFundHotKey{Operator: thief.String(), NodeId: "node-1", Amount: math.NewInt(1), Source: bank})
	require.Error(t, err)

	_, err = f.Msg.RetireNode(f.Ctx, &types.MsgRetireNode{Operator: op.String(), NodeId: "node-1"})
	require.NoError(t, err)
	opBank := f.Bank.balanceOf(op.String())
	_, err = f.Msg.FundHotKey(f.Ctx, &types.MsgFundHotKey{Operator: op.String(), NodeId: "node-1", Amount: math.NewInt(1), Source: bank})
	require.Error(t, err)

	require.True(t, f.Bank.balanceOf(op.String()).Equal(opBank))
	require.True(t, f.Earnings.feeBalanceOf(hot).IsZero())
}
