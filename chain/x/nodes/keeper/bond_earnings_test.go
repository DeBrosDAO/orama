package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DeBrosOfficial/network/chain/x/nodes/types"
)

// Bonding from earnings happens inside MsgBondNode, after its own checks. A bond the handler
// rejects therefore never turns earnings into a bank balance.
func TestBondNode_fundsShortfallFromEarnings(t *testing.T) {
	f := newTestFixture(t)
	op, _ := storageNode(t, f, "n", nil, 0)
	before := f.Bank.balanceOf(op.String())
	f.Earnings.balances[op.String()] = orama(5)

	_, err := f.Msg.BondNode(f.Ctx, &types.MsgBondNode{Operator: op.String(), NodeId: "n", Role: types.RoleStorage, Amount: before.Add(orama(2))})
	require.NoError(t, err)

	require.True(t, f.Earnings.balanceOf(op).Equal(orama(3)), "the 2 ORAMA the bank lacked came from earnings")
	require.True(t, f.Bank.balanceOf(op.String()).IsZero())
	f.requireInvariants(t)
}

func TestBondNode_rejectedBondLeavesEarningsAndBankAlone(t *testing.T) {
	f := newTestFixture(t)
	op, _ := storageNode(t, f, "n", nil, 0)
	other := newAccount(t)
	f.registerOperator(t, other)
	f.Earnings.balances[other.String()] = orama(5)
	f.Earnings.balances[op.String()] = orama(5)
	bank := f.Bank.balanceOf(op.String())
	big := bank.Add(orama(2))

	requireUntouched := func(t *testing.T, who string, err error) {
		t.Helper()
		require.Error(t, err)
		require.True(t, f.Earnings.balances[who].Equal(orama(5)), "earnings moved for a bond that failed")
		if who == op.String() {
			require.True(t, f.Bank.balanceOf(who).Equal(bank), "bank balance moved for a bond that failed")
		}
	}

	_, err := f.Msg.BondNode(f.Ctx, &types.MsgBondNode{Operator: op.String(), NodeId: "ghost", Role: types.RoleStorage, Amount: big})
	requireUntouched(t, op.String(), err)
	_, err = f.Msg.BondNode(f.Ctx, &types.MsgBondNode{Operator: other.String(), NodeId: "n", Role: types.RoleStorage, Amount: big})
	requireUntouched(t, other.String(), err)
	_, err = f.Msg.BondNode(f.Ctx, &types.MsgBondNode{Operator: op.String(), NodeId: "n", Role: types.RoleRelay, Amount: big})
	requireUntouched(t, op.String(), err)

	_, err = f.Msg.RetireNode(f.Ctx, &types.MsgRetireNode{Operator: op.String(), NodeId: "n"})
	require.NoError(t, err)
	bank = f.Bank.balanceOf(op.String())
	_, err = f.Msg.BondNode(f.Ctx, &types.MsgBondNode{Operator: op.String(), NodeId: "n", Role: types.RoleStorage, Amount: bank.Add(orama(2))})
	requireUntouched(t, op.String(), err)
}

func TestBondNode_insufficientEarningsMoveNothing(t *testing.T) {
	f := newTestFixture(t)
	op, _ := storageNode(t, f, "n", nil, 0)
	bank := f.Bank.balanceOf(op.String())
	f.Earnings.balances[op.String()] = orama(1)

	_, err := f.Msg.BondNode(f.Ctx, &types.MsgBondNode{Operator: op.String(), NodeId: "n", Role: types.RoleStorage, Amount: bank.Add(orama(2))})
	require.Error(t, err)
	require.True(t, f.Earnings.balances[op.String()].Equal(orama(1)), "a partial top-up cannot make the bond succeed, so none is made")
	require.True(t, f.Bank.balanceOf(op.String()).Equal(bank))
}

// CheckBondNode is the dry run x/shielded makes when a node-bond unshield is made: it refuses
// what BondNode would refuse for the node, the operator and the role, and changes nothing.
func TestCheckBondNode_refusesWhatBondNodeRefusesAndChangesNothing(t *testing.T) {
	f := newTestFixture(t)
	op, _ := storageNode(t, f, "n", nil, 0)
	other := newAccount(t)
	f.registerOperator(t, other)
	bank := f.Bank.balanceOf(op.String())
	before, err := f.Keeper.GetNode(f.Ctx, "n")
	require.NoError(t, err)

	good := &types.MsgBondNode{Operator: op.String(), NodeId: "n", Role: types.RoleStorage, Amount: orama(2)}
	require.NoError(t, f.Keeper.CheckBondNode(f.Ctx, good), "the operator's balance is not the check's business")

	for name, msg := range map[string]*types.MsgBondNode{
		"unknown node":   {Operator: op.String(), NodeId: "ghost", Role: types.RoleStorage, Amount: orama(2)},
		"someone else's": {Operator: other.String(), NodeId: "n", Role: types.RoleStorage, Amount: orama(2)},
		"role not held":  {Operator: op.String(), NodeId: "n", Role: types.RoleRelay, Amount: orama(2)},
		"zero amount":    {Operator: op.String(), NodeId: "n", Role: types.RoleStorage, Amount: orama(0)},
		"no operator":    {NodeId: "n", Role: types.RoleStorage, Amount: orama(2)},
	} {
		require.Error(t, f.Keeper.CheckBondNode(f.Ctx, msg), name)
	}
	require.Error(t, f.Keeper.CheckBondNode(f.Ctx, nil))

	after, err := f.Keeper.GetNode(f.Ctx, "n")
	require.NoError(t, err)
	require.Equal(t, before, after, "no bond changed")
	require.True(t, f.Bank.balanceOf(op.String()).Equal(bank), "no coin moved")

	_, err = f.Msg.RetireNode(f.Ctx, &types.MsgRetireNode{Operator: op.String(), NodeId: "n"})
	require.NoError(t, err)
	require.Error(t, f.Keeper.CheckBondNode(f.Ctx, good), "a retired node takes no bond")
}
