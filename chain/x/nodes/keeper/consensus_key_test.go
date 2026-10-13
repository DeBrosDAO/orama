package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DeBrosOfficial/network/chain/x/nodes/types"
)

func TestOperatorOfConsensusKey_namesTheOperatorOfTheNodeThatBindsTheKey(t *testing.T) {
	f := newTestFixture(t)
	op, hot := newAccount(t), newAccount(t)
	f.fund(op, 100)
	f.registerOperator(t, op)
	consensus := edBinding(t, testChainID, op.String(), types.ConsensusService)
	f.registerNode(t, op, hot, "node-1", []types.Role{types.RoleValidator}, []types.Binding{consensus})

	got, ok, err := f.Keeper.OperatorOfConsensusKey(f.Ctx, consensus.Pubkey)

	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, op.String(), got)
}

func TestOperatorOfConsensusKey_oneOperatorOwnsTheKeysOfAllItsNodes(t *testing.T) {
	f := newTestFixture(t)
	op, hotA, hotB := newAccount(t), newAccount(t), newAccount(t)
	f.fund(op, 100)
	f.registerOperator(t, op)
	a := edBinding(t, testChainID, op.String(), types.ConsensusService)
	b := edBinding(t, testChainID, op.String(), types.ConsensusService)
	f.registerNode(t, op, hotA, "node-a", []types.Role{types.RoleValidator}, []types.Binding{a})
	f.registerNode(t, op, hotB, "node-b", []types.Role{types.RoleValidator}, []types.Binding{b})

	for _, pub := range [][]byte{a.Pubkey, b.Pubkey} {
		got, ok, err := f.Keeper.OperatorOfConsensusKey(f.Ctx, pub)
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, op.String(), got)
	}
}

func TestOperatorOfConsensusKey_anUnknownKeyHasNoOperator(t *testing.T) {
	f := newTestFixture(t)

	got, ok, err := f.Keeper.OperatorOfConsensusKey(f.Ctx, make([]byte, 32))

	require.NoError(t, err)
	require.False(t, ok)
	require.Empty(t, got)
}

func TestOperatorOfConsensusKey_aKeyBoundUnderAnotherServiceHasNoOperator(t *testing.T) {
	f := newTestFixture(t)
	op, hot := newAccount(t), newAccount(t)
	f.fund(op, 100)
	f.registerOperator(t, op)
	tor := edBinding(t, testChainID, op.String(), "tor")
	f.registerNode(t, op, hot, "node-1", []types.Role{types.RoleRelay}, []types.Binding{tor})

	_, ok, err := f.Keeper.OperatorOfConsensusKey(f.Ctx, tor.Pubkey)

	require.NoError(t, err)
	require.False(t, ok, "only a consensus binding attributes a validator to an operator")
}

func TestOperatorOfConsensusKey_aRetiredNodeNamesNoOperator(t *testing.T) {
	f := newTestFixture(t)
	op, hot := newAccount(t), newAccount(t)
	f.fund(op, 100)
	f.registerOperator(t, op)
	consensus := edBinding(t, testChainID, op.String(), types.ConsensusService)
	f.registerNode(t, op, hot, "node-1", []types.Role{types.RoleValidator}, []types.Binding{consensus})

	_, err := f.Msg.RetireNode(f.Ctx, &types.MsgRetireNode{Operator: op.String(), NodeId: "node-1"})
	require.NoError(t, err)

	_, ok, err := f.Keeper.OperatorOfConsensusKey(f.Ctx, consensus.Pubkey)
	require.NoError(t, err)
	require.False(t, ok, "a retired node released its keys, so its validator is unlinked again")
}

func TestOperatorOfConsensusKey_aRotatedAwayKeyNamesNoOperator(t *testing.T) {
	f := newTestFixture(t)
	op, hot := newAccount(t), newAccount(t)
	f.fund(op, 100)
	f.registerOperator(t, op)
	old := edBinding(t, testChainID, op.String(), types.ConsensusService)
	f.registerNode(t, op, hot, "node-1", []types.Role{types.RoleValidator}, []types.Binding{old})
	fresh := edBinding(t, testChainID, op.String(), types.ConsensusService)

	_, err := f.Msg.UpdateNode(f.Ctx, &types.MsgUpdateNode{
		Operator: op.String(), NodeId: "node-1", Bindings: withHot(t, op, hot, fresh),
	})
	require.NoError(t, err)

	_, ok, err := f.Keeper.OperatorOfConsensusKey(f.Ctx, old.Pubkey)
	require.NoError(t, err)
	require.False(t, ok)
	_, ok, err = f.Keeper.OperatorOfConsensusKey(f.Ctx, fresh.Pubkey)
	require.NoError(t, err)
	require.True(t, ok)
}

func TestRegisterNode_refusesAConsensusBindingThatIsNotEd25519(t *testing.T) {
	f := newTestFixture(t)
	op, hot := newAccount(t), newAccount(t)
	f.fund(op, 100)
	f.registerOperator(t, op)

	_, err := f.Msg.RegisterNode(f.Ctx, &types.MsgRegisterNode{
		Operator: op.String(), NodeId: "node-bad", Roles: []types.Role{types.RoleValidator},
		HotKey: hot.String(), Bindings: withHot(t, op, hot, secpBinding(t, testChainID, op.String(), types.ConsensusService)),
	})

	require.ErrorIs(t, err, types.ErrConsensusKey)
	_, err = f.Keeper.GetNode(f.Ctx, "node-bad")
	require.ErrorIs(t, err, types.ErrNotFound)
}

func TestRegisterNode_refusesTwoConsensusBindings(t *testing.T) {
	f := newTestFixture(t)
	op, hot := newAccount(t), newAccount(t)
	f.fund(op, 100)
	f.registerOperator(t, op)

	_, err := f.Msg.RegisterNode(f.Ctx, &types.MsgRegisterNode{
		Operator: op.String(), NodeId: "node-bad", Roles: []types.Role{types.RoleValidator},
		HotKey: hot.String(), Bindings: withHot(t, op, hot,
			edBinding(t, testChainID, op.String(), types.ConsensusService),
			edBinding(t, testChainID, op.String(), types.ConsensusService)),
	})

	require.Error(t, err, "a node binds one consensus key")
}

func TestUpdateNode_refusesAConsensusBindingThatIsNotEd25519(t *testing.T) {
	f := newTestFixture(t)
	op, hot := newAccount(t), newAccount(t)
	f.fund(op, 100)
	f.registerOperator(t, op)
	f.registerNode(t, op, hot, "node-1", []types.Role{types.RoleValidator}, nil)

	_, err := f.Msg.UpdateNode(f.Ctx, &types.MsgUpdateNode{
		Operator: op.String(), NodeId: "node-1",
		Bindings: withHot(t, op, hot, secpBinding(t, testChainID, op.String(), types.ConsensusService)),
	})

	require.ErrorIs(t, err, types.ErrConsensusKey)
}

func TestRegisterNode_aConsensusKeyBelongsToOneNode(t *testing.T) {
	f := newTestFixture(t)
	op, other, hotA, hotB := newAccount(t), newAccount(t), newAccount(t), newAccount(t)
	f.fund(op, 100)
	f.fund(other, 100)
	f.registerOperator(t, op)
	f.registerOperator(t, other)
	consensus := edBinding(t, testChainID, op.String(), types.ConsensusService)
	f.registerNode(t, op, hotA, "node-a", []types.Role{types.RoleValidator}, []types.Binding{consensus})

	// Another operator cannot claim the same key: it is live on node-a, and the binding it would
	// sign names a different operator, so the signature cannot match either.
	_, err := f.Msg.RegisterNode(f.Ctx, &types.MsgRegisterNode{
		Operator: other.String(), NodeId: "node-b", Roles: []types.Role{types.RoleValidator},
		HotKey: hotB.String(), Bindings: withHot(t, other, hotB, consensus),
	})
	require.Error(t, err)
}
