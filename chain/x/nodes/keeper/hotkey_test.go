package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/nodes/types"
)

func registerMsg(t *testing.T, op, hot sdk.AccAddress, id string, bindings ...types.Binding) *types.MsgRegisterNode {
	t.Helper()
	return &types.MsgRegisterNode{
		Operator: op.String(), NodeId: id, Roles: []types.Role{types.RoleRelay},
		HotKey: hot.String(), Bindings: bindings,
	}
}

func TestRegisterNode_hotKeyMustProveItself(t *testing.T) {
	f := newTestFixture(t)
	op, hot := newAccount(t), newAccount(t)
	f.fund(op, 100)
	f.registerOperator(t, op)
	tor := edBinding(t, testChainID, op.String(), "tor")

	// The operator names an address but has no signature from it.
	_, err := f.Msg.RegisterNode(f.Ctx, registerMsg(t, op, hot, "no-proof", tor))
	require.ErrorIs(t, err, types.ErrHotKey)

	// A hot-key binding signed by a different key than the named hot key.
	stranger := newAccount(t)
	_, err = f.Msg.RegisterNode(f.Ctx, registerMsg(t, op, hot, "wrong-key", tor, hotBinding(t, testChainID, op, stranger)))
	require.ErrorIs(t, err, types.ErrHotKey)

	// Right key, but signed for another operator: the signature does not verify for this one.
	other := newAccount(t)
	_, err = f.Msg.RegisterNode(f.Ctx, registerMsg(t, op, hot, "wrong-operator", tor, hotBinding(t, testChainID, other, hot)))
	require.ErrorIs(t, err, types.ErrInvalidBinding)

	// The hot-key binding must be secp256k1.
	edHot := edBinding(t, testChainID, op.String(), types.HotKeyService)
	_, err = f.Msg.RegisterNode(f.Ctx, registerMsg(t, op, hot, "ed-hot", tor, edHot))
	require.ErrorIs(t, err, types.ErrHotKey)

	// Two hot-key bindings are not allowed.
	_, err = f.Msg.RegisterNode(f.Ctx, registerMsg(t, op, hot, "two", tor, hotBinding(t, testChainID, op, hot), hotBinding(t, testChainID, op, stranger)))
	require.Error(t, err)

	_, err = f.Msg.RegisterNode(f.Ctx, registerMsg(t, op, hot, "ok", tor, hotBinding(t, testChainID, op, hot)))
	require.NoError(t, err)
	f.requireInvariants(t)
}

func TestRegisterNode_hotKeyCannotBeAnotherNodesHotKeyOrAnOperator(t *testing.T) {
	f := newTestFixture(t)
	opA, opB, hot := newAccount(t), newAccount(t), newAccount(t)
	f.fund(opA, 100)
	f.registerOperator(t, opA)
	f.fund(opB, 100)
	f.registerOperator(t, opB)
	_, err := f.Msg.RegisterNode(f.Ctx, registerMsg(t, opA, hot, "a", edBinding(t, testChainID, opA.String(), "tor"), hotBinding(t, testChainID, opA, hot)))
	require.NoError(t, err)

	// Another operator names the same hot key, with a signature the hot key really made for it.
	_, err = f.Msg.RegisterNode(f.Ctx, registerMsg(t, opB, hot, "b", edBinding(t, testChainID, opB.String(), "tor"), hotBinding(t, testChainID, opB, hot)))
	require.ErrorIs(t, err, types.ErrHotKey)

	// A registered operator is not a hot key, even one that can sign for itself.
	_, err = f.Msg.RegisterNode(f.Ctx, registerMsg(t, opA, opB, "c", edBinding(t, testChainID, opA.String(), "ipfs"), hotBinding(t, testChainID, opA, opB)))
	require.ErrorIs(t, err, types.ErrHotKey)
}

func TestRegisterOperator_refusesALiveHotKey(t *testing.T) {
	f := newTestFixture(t)
	op, hot := newAccount(t), newAccount(t)
	f.fund(op, 100)
	f.registerOperator(t, op)
	f.registerNode(t, op, hot, "n", []types.Role{types.RoleRelay}, []types.Binding{edBinding(t, testChainID, op.String(), "tor")})

	_, err := f.Msg.RegisterOperator(f.Ctx, &types.MsgRegisterOperator{Operator: hot.String()})
	require.ErrorIs(t, err, types.ErrHotKey)

	_, err = f.Msg.RetireNode(f.Ctx, &types.MsgRetireNode{Operator: op.String(), NodeId: "n"})
	require.NoError(t, err)
	_, err = f.Msg.RegisterOperator(f.Ctx, &types.MsgRegisterOperator{Operator: hot.String()})
	require.NoError(t, err, "a retired node's hot key is released")
}

func TestUpdateNode_hotKeyChangeNeedsItsOwnProof(t *testing.T) {
	f := newTestFixture(t)
	op, hot, next := newAccount(t), newAccount(t), newAccount(t)
	f.fund(op, 100)
	f.registerOperator(t, op)
	f.registerNode(t, op, hot, "n", []types.Role{types.RoleRelay}, []types.Binding{edBinding(t, testChainID, op.String(), "tor")})

	// The operator points the hot key at an address it does not control: no binding to prove it.
	_, err := f.Msg.UpdateNode(f.Ctx, &types.MsgUpdateNode{Operator: op.String(), NodeId: "n", HotKey: next.String()})
	require.ErrorIs(t, err, types.ErrHotKey)

	// A proof from a different key than the one named.
	_, err = f.Msg.UpdateNode(f.Ctx, &types.MsgUpdateNode{
		Operator: op.String(), NodeId: "n", HotKey: next.String(),
		Bindings: withHot(t, op, newAccount(t), edBinding(t, testChainID, op.String(), "tor")),
	})
	require.ErrorIs(t, err, types.ErrHotKey)

	// A hot key that belongs to another node is refused even with a valid proof.
	opB, hotB := newAccount(t), newAccount(t)
	f.fund(opB, 100)
	f.registerOperator(t, opB)
	f.registerNode(t, opB, hotB, "b", []types.Role{types.RoleRelay}, []types.Binding{edBinding(t, testChainID, opB.String(), "tor")})
	_, err = f.Msg.UpdateNode(f.Ctx, &types.MsgUpdateNode{
		Operator: op.String(), NodeId: "n", HotKey: hotB.String(),
		Bindings: withHot(t, op, hotB, edBinding(t, testChainID, op.String(), "tor")),
	})
	require.ErrorIs(t, err, types.ErrHotKey)

	// Replacing the bindings must keep the current hot key's own binding.
	_, err = f.Msg.UpdateNode(f.Ctx, &types.MsgUpdateNode{
		Operator: op.String(), NodeId: "n", Bindings: []types.Binding{edBinding(t, testChainID, op.String(), "tor")},
	})
	require.ErrorIs(t, err, types.ErrHotKey)

	// A proper rotation: the old hot key is released and the new one is indexed.
	_, err = f.Msg.UpdateNode(f.Ctx, &types.MsgUpdateNode{
		Operator: op.String(), NodeId: "n", HotKey: next.String(),
		Bindings: withHot(t, op, next, edBinding(t, testChainID, op.String(), "tor")),
	})
	require.NoError(t, err)
	_, err = f.Msg.RegisterOperator(f.Ctx, &types.MsgRegisterOperator{Operator: hot.String()})
	require.NoError(t, err, "the old hot key is no longer a hot key")
	_, err = f.Msg.RegisterOperator(f.Ctx, &types.MsgRegisterOperator{Operator: next.String()})
	require.ErrorIs(t, err, types.ErrHotKey)
	f.requireInvariants(t)
}

func TestGenesis_hotKeyRules(t *testing.T) {
	f := newTestFixture(t)
	op, hot := newAccount(t), newAccount(t)
	f.fund(op, 100)
	f.registerOperator(t, op)
	f.registerNode(t, op, hot, "n", []types.Role{types.RoleRelay}, []types.Binding{edBinding(t, testChainID, op.String(), "tor")})
	exported, err := f.Keeper.ExportGenesis(f.Ctx)
	require.NoError(t, err)
	require.NoError(t, exported.Validate())

	noProof := *exported
	noProof.Nodes = append([]types.Node(nil), exported.Nodes...)
	noProof.Nodes[0].Bindings = []types.Binding{edBinding(t, testChainID, op.String(), "tor")}
	require.ErrorIs(t, noProof.Validate(), types.ErrHotKey, "a live node without its hot-key binding is refused")

	dup := *exported
	dup.Nodes = append([]types.Node(nil), exported.Nodes...)
	second := exported.Nodes[0]
	second.NodeId = "n2"
	second.Bindings = []types.Binding{edBinding(t, testChainID, op.String(), "tor"), exported.Nodes[0].Bindings[0]}
	dup.Nodes = append(dup.Nodes, second)
	require.Error(t, dup.Validate(), "two live nodes cannot share a hot key")

	asOperator := *exported
	asOperator.Nodes = append([]types.Node(nil), exported.Nodes...)
	asOperator.Operators = append([]types.Operator(nil), exported.Operators...)
	asOperator.Operators = append(asOperator.Operators, types.Operator{Address: hot.String()})
	require.ErrorIs(t, asOperator.Validate(), types.ErrHotKey, "a hot key cannot also be an operator")
}
