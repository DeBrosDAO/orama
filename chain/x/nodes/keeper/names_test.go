package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/query"

	"github.com/DeBrosOfficial/network/chain/x/nodes/keeper"
	"github.com/DeBrosOfficial/network/chain/x/nodes/types"
)

// nameFixture is an operator with 10 ORAMA and one registered node.
type nameFixture struct {
	*testFixture
	op, hot sdk.AccAddress
}

func newNameFixture(t *testing.T) *nameFixture {
	t.Helper()
	f := newTestFixture(t)
	op, hot := newAccount(t), newAccount(t)
	f.fund(op, 10)
	f.registerOperator(t, op)
	f.registerNode(t, op, hot, "node-1", []types.Role{types.RoleStorage}, []types.Binding{edBinding(t, testChainID, op.String(), "ipfs")})
	return &nameFixture{testFixture: f, op: op, hot: hot}
}

func (n *nameFixture) claim(t *testing.T, nodeID, name string) error {
	t.Helper()
	_, err := n.Msg.ClaimNodeName(n.Ctx, &types.MsgClaimNodeName{Operator: n.op.String(), NodeId: nodeID, Name: name})
	return err
}

func (n *nameFixture) deposit() math.Int { return types.DefaultNameDeposit }

func TestClaimNodeName_locksTheDepositAndIndexesTheName(t *testing.T) {
	n := newNameFixture(t)
	bankBefore := n.Bank.balanceOf(n.op.String())
	moduleBefore := n.Bank.balanceOf(types.ModuleName)

	require.NoError(t, n.claim(t, "node-1", "alpha"))

	require.True(t, bankBefore.Sub(n.Bank.balanceOf(n.op.String())).Equal(n.deposit()), "the operator paid the deposit")
	require.True(t, n.Bank.balanceOf(types.ModuleName).Sub(moduleBefore).Equal(n.deposit()), "the nodes module holds it")
	got, err := n.Keeper.Names.Get(n.Ctx, "alpha")
	require.NoError(t, err)
	require.Equal(t, types.NodeName{Name: "alpha", NodeId: "node-1", Operator: n.op.String(), Deposit: n.deposit()}, got)
	held, err := n.Keeper.NodeNames.Get(n.Ctx, "node-1")
	require.NoError(t, err)
	require.Equal(t, "alpha", held)
	n.requireInvariants(t)
}

func TestClaimNodeName_emitsAClaimEvent(t *testing.T) {
	n := newNameFixture(t)
	require.NoError(t, n.claim(t, "node-1", "alpha"))

	var found *sdk.Event
	for _, e := range n.Ctx.EventManager().Events() {
		if e.Type == types.EventTypeClaimNodeName {
			ev := sdk.Event(e)
			found = &ev
		}
	}
	require.NotNil(t, found)
	attrs := map[string]string{}
	for _, a := range found.Attributes {
		attrs[a.Key] = a.Value
	}
	require.Equal(t, "alpha", attrs[types.AttributeName])
	require.Equal(t, "node-1", attrs[types.AttributeNodeID])
	require.Equal(t, n.op.String(), attrs[types.AttributeOperator])
	require.Equal(t, n.deposit().String(), attrs[types.AttributeDeposit])
}

func TestClaimNodeName_isFirstComeFirstServed(t *testing.T) {
	n := newNameFixture(t)
	other, otherHot := newAccount(t), newAccount(t)
	n.fund(other, 10)
	n.registerOperator(t, other)
	n.registerNode(t, other, otherHot, "node-2", []types.Role{types.RoleStorage}, []types.Binding{edBinding(t, testChainID, other.String(), "ipfs")})
	require.NoError(t, n.claim(t, "node-1", "alpha"))
	otherBefore := n.Bank.balanceOf(other.String())

	_, err := n.Msg.ClaimNodeName(n.Ctx, &types.MsgClaimNodeName{Operator: other.String(), NodeId: "node-2", Name: "alpha"})

	require.ErrorIs(t, err, types.ErrNameTaken)
	require.True(t, n.Bank.balanceOf(other.String()).Equal(otherBefore), "a refused claim locked nothing")
	held, gerr := n.Keeper.Names.Get(n.Ctx, "alpha")
	require.NoError(t, gerr)
	require.Equal(t, "node-1", held.NodeId, "the first claim keeps the name")
}

func TestClaimNodeName_aNodeHoldsOneName(t *testing.T) {
	n := newNameFixture(t)
	require.NoError(t, n.claim(t, "node-1", "alpha"))

	err := n.claim(t, "node-1", "beta")

	require.ErrorIs(t, err, types.ErrNodeHasName)
	_, err = n.Keeper.Names.Get(n.Ctx, "beta")
	require.Error(t, err, "the second name was not indexed")
}

func TestClaimNodeName_refusesAnInvalidOrReservedName(t *testing.T) {
	n := newNameFixture(t)
	for _, name := range []string{"ab", "Alpha", "-alpha", "alpha-", "seed", "seed3", "www", "ns1", "xn--alpha", "a.b"} {
		require.ErrorIs(t, n.claim(t, "node-1", name), types.ErrInvalidName, name)
	}
	_, err := n.Keeper.NodeNames.Get(n.Ctx, "node-1")
	require.Error(t, err, "no refused claim left a name behind")
}

func TestClaimNodeName_onlyTheNodesOperatorMayClaim(t *testing.T) {
	n := newNameFixture(t)
	other := newAccount(t)
	n.fund(other, 10)
	n.registerOperator(t, other)

	_, err := n.Msg.ClaimNodeName(n.Ctx, &types.MsgClaimNodeName{Operator: other.String(), NodeId: "node-1", Name: "alpha"})

	require.ErrorIs(t, err, types.ErrUnauthorized)
	require.NoError(t, n.claim(t, "node-1", "alpha"), "the name is still free for the right operator")
}

func TestClaimNodeName_refusesAnUnknownNode(t *testing.T) {
	n := newNameFixture(t)
	require.ErrorIs(t, n.claim(t, "ghost", "alpha"), types.ErrNotFound)
}

func TestClaimNodeName_refusesARetiredNode(t *testing.T) {
	n := newNameFixture(t)
	_, err := n.Msg.RetireNode(n.Ctx, &types.MsgRetireNode{Operator: n.op.String(), NodeId: "node-1"})
	require.NoError(t, err)

	require.Error(t, n.claim(t, "node-1", "alpha"))
}

func TestClaimNodeName_needsTheDeposit(t *testing.T) {
	f := newTestFixture(t)
	op, hot := newAccount(t), newAccount(t)
	f.fund(op, 1)
	f.registerOperator(t, op)
	f.registerNode(t, op, hot, "node-1", []types.Role{types.RoleStorage}, []types.Binding{edBinding(t, testChainID, op.String(), "ipfs")})
	f.Bank.balances[op.String()] = math.ZeroInt()
	n := &nameFixture{testFixture: f, op: op, hot: hot}

	err := n.claim(t, "node-1", "alpha")

	require.Error(t, err, "an operator with no balance cannot lock the deposit")
	_, gerr := n.Keeper.Names.Get(n.Ctx, "alpha")
	require.Error(t, gerr)
}

func TestClaimNodeName_topsTheDepositUpFromEarnings(t *testing.T) {
	f := newTestFixture(t)
	op, hot := newAccount(t), newAccount(t)
	f.fund(op, 5)
	f.registerOperator(t, op)
	f.registerNode(t, op, hot, "node-1", []types.Role{types.RoleStorage}, []types.Binding{edBinding(t, testChainID, op.String(), "ipfs")})
	f.Bank.balances[op.String()] = math.ZeroInt()
	f.Earnings.balances[op.String()] = types.DefaultNameDeposit
	n := &nameFixture{testFixture: f, op: op, hot: hot}

	require.NoError(t, n.claim(t, "node-1", "alpha"))

	require.True(t, f.Earnings.balanceOf(op).IsZero(), "the deposit came out of the operator's own earnings")
}

func TestReleaseNodeName_returnsTheDepositAndFreesTheName(t *testing.T) {
	n := newNameFixture(t)
	require.NoError(t, n.claim(t, "node-1", "alpha"))
	bankBefore := n.Bank.balanceOf(n.op.String())

	_, err := n.Msg.ReleaseNodeName(n.Ctx, &types.MsgReleaseNodeName{Operator: n.op.String(), NodeId: "node-1"})

	require.NoError(t, err)
	require.True(t, n.Bank.balanceOf(n.op.String()).Sub(bankBefore).Equal(n.deposit()), "the full deposit came back")
	_, err = n.Keeper.Names.Get(n.Ctx, "alpha")
	require.Error(t, err)
	_, err = n.Keeper.NodeNames.Get(n.Ctx, "node-1")
	require.Error(t, err)
	n.requireInvariants(t)
	require.NoError(t, n.claim(t, "node-1", "alpha"), "a released name can be claimed again")
}

func TestReleaseNodeName_needsAName(t *testing.T) {
	n := newNameFixture(t)
	_, err := n.Msg.ReleaseNodeName(n.Ctx, &types.MsgReleaseNodeName{Operator: n.op.String(), NodeId: "node-1"})
	require.ErrorIs(t, err, types.ErrNoName)
}

func TestReleaseNodeName_onlyTheNodesOperatorMayRelease(t *testing.T) {
	n := newNameFixture(t)
	require.NoError(t, n.claim(t, "node-1", "alpha"))
	other := newAccount(t)
	n.registerOperator(t, other)

	_, err := n.Msg.ReleaseNodeName(n.Ctx, &types.MsgReleaseNodeName{Operator: other.String(), NodeId: "node-1"})

	require.ErrorIs(t, err, types.ErrUnauthorized)
	held, gerr := n.Keeper.NodeNames.Get(n.Ctx, "node-1")
	require.NoError(t, gerr)
	require.Equal(t, "alpha", held)
}

func TestRetireNode_releasesTheNameAndRefundsTheDeposit(t *testing.T) {
	n := newNameFixture(t)
	require.NoError(t, n.claim(t, "node-1", "alpha"))
	bankBefore := n.Bank.balanceOf(n.op.String())

	_, err := n.Msg.RetireNode(n.Ctx, &types.MsgRetireNode{Operator: n.op.String(), NodeId: "node-1"})

	require.NoError(t, err)
	_, err = n.Keeper.Names.Get(n.Ctx, "alpha")
	require.Error(t, err, "the retired node's name is free")
	require.True(t, n.Bank.balanceOf(n.op.String()).Sub(bankBefore).GTE(n.deposit()), "the deposit came back with the state deposit refund")
	n.requireInvariants(t)
}

func TestTombstone_releasesTheNameAndRefundsTheDeposit(t *testing.T) {
	n := newNameFixture(t)
	require.NoError(t, n.claim(t, "node-1", "alpha"))
	moduleBefore := n.Bank.balanceOf(types.ModuleName)

	require.NoError(t, n.Keeper.Tombstone(n.Ctx, "node-1"))

	_, err := n.Keeper.Names.Get(n.Ctx, "alpha")
	require.Error(t, err)
	require.True(t, moduleBefore.Sub(n.Bank.balanceOf(types.ModuleName)).Equal(n.deposit()), "the module paid the deposit back")
	n.requireInvariants(t)
}

func TestNodeNameQueries_answerByNameByNodeAndAsAList(t *testing.T) {
	n := newNameFixture(t)
	q := keeper.NewQueryServerImpl(n.Keeper)
	require.NoError(t, n.claim(t, "node-1", "alpha"))
	_, err := n.Msg.UpdateNode(n.Ctx, &types.MsgUpdateNode{
		Operator: n.op.String(), NodeId: "node-1", SetEndpoints: true,
		Endpoints: []string{"https://8.8.8.8:443", "https://host.example:443", "https://[2606:4700:4700::1111]:443"},
	})
	require.NoError(t, err)

	byName, err := q.NodeByName(n.Ctx, &types.QueryNodeByNameRequest{Name: "alpha"})
	require.NoError(t, err)
	require.Equal(t, "node-1", byName.Node.NodeId)
	require.Equal(t, n.op.String(), byName.Node.Operator)
	require.Len(t, byName.Node.Ips, 2, "only the literal IPs of the endpoints")

	ofNode, err := q.NameOfNode(n.Ctx, &types.QueryNameOfNodeRequest{NodeId: "node-1"})
	require.NoError(t, err)
	require.Equal(t, "alpha", ofNode.Name.Name)
	require.True(t, ofNode.Name.Deposit.Equal(n.deposit()))

	_, err = q.NodeByName(n.Ctx, &types.QueryNodeByNameRequest{Name: "nobody"})
	require.Error(t, err)
	_, err = q.NameOfNode(n.Ctx, &types.QueryNameOfNodeRequest{NodeId: "ghost"})
	require.Error(t, err)
	_, err = q.NodeByName(n.Ctx, &types.QueryNodeByNameRequest{})
	require.Error(t, err)
}

func TestNodeNames_pagesInNameOrder(t *testing.T) {
	f := newTestFixture(t)
	q := keeper.NewQueryServerImpl(f.Keeper)
	op := newAccount(t)
	f.fund(op, 100)
	f.registerOperator(t, op)
	names := []string{"delta", "alpha", "charlie", "bravo", "echo"}
	for _, name := range names {
		hot := newAccount(t)
		id := "node-" + name
		_, err := f.Msg.RegisterNode(f.Ctx, &types.MsgRegisterNode{
			Operator: op.String(), NodeId: id, Roles: []types.Role{types.RoleStorage}, HotKey: hot.String(),
			Bindings: withHot(t, op, hot, edBinding(t, testChainID, op.String(), "ipfs")),
		})
		require.NoError(t, err)
		_, err = f.Msg.ClaimNodeName(f.Ctx, &types.MsgClaimNodeName{Operator: op.String(), NodeId: id, Name: name})
		require.NoError(t, err)
	}

	var got []string
	var key []byte
	for pages := 0; pages < 10; pages++ {
		res, err := q.NodeNames(f.Ctx, &types.QueryNodeNamesRequest{Pagination: &query.PageRequest{Key: key, Limit: 2}})
		require.NoError(t, err)
		require.LessOrEqual(t, len(res.Nodes), 2)
		for _, node := range res.Nodes {
			got = append(got, node.Name)
		}
		key = res.Pagination.NextKey
		if key == nil {
			break
		}
	}
	require.Equal(t, []string{"alpha", "bravo", "charlie", "delta", "echo"}, got, "every name once, in name order")
}

func TestNodeNames_emptyListIsEmptyNotAnError(t *testing.T) {
	f := newTestFixture(t)
	res, err := keeper.NewQueryServerImpl(f.Keeper).NodeNames(f.Ctx, &types.QueryNodeNamesRequest{})
	require.NoError(t, err)
	require.NotNil(t, res.Nodes)
	require.Empty(t, res.Nodes)
}

func TestNodeNamesGenesis_roundTrips(t *testing.T) {
	n := newNameFixture(t)
	require.NoError(t, n.claim(t, "node-1", "alpha"))

	exported, err := n.Keeper.ExportGenesis(n.Ctx)
	require.NoError(t, err)
	require.Len(t, exported.NodeNames, 1)
	require.NoError(t, exported.Validate())

	fresh := newRawFixture(t)
	fresh.Bank.fund(types.ModuleName, n.Bank.balanceOf(types.ModuleName))
	require.NoError(t, fresh.Keeper.InitGenesis(fresh.Ctx, *exported))
	got, err := fresh.Keeper.Names.Get(fresh.Ctx, "alpha")
	require.NoError(t, err)
	require.Equal(t, "node-1", got.NodeId)
	held, err := fresh.Keeper.NodeNames.Get(fresh.Ctx, "node-1")
	require.NoError(t, err)
	require.Equal(t, "alpha", held)
}

func TestNodeNamesGenesis_validateRefusesWhatCannotHoldTogether(t *testing.T) {
	n := newNameFixture(t)
	require.NoError(t, n.claim(t, "node-1", "alpha"))
	good, err := n.Keeper.ExportGenesis(n.Ctx)
	require.NoError(t, err)
	claim := good.NodeNames[0]

	cases := map[string]func(gs *types.GenesisState){
		"a reserved name":       func(gs *types.GenesisState) { gs.NodeNames[0].Name = "seed1" },
		"an unknown node":       func(gs *types.GenesisState) { gs.NodeNames[0].NodeId = "ghost" },
		"another operator":      func(gs *types.GenesisState) { gs.NodeNames[0].Operator = newAccount(t).String() },
		"a zero deposit":        func(gs *types.GenesisState) { gs.NodeNames[0].Deposit = math.ZeroInt() },
		"a duplicate name":      func(gs *types.GenesisState) { gs.NodeNames = append(gs.NodeNames, claim) },
		"a retired node's name": func(gs *types.GenesisState) { gs.Nodes[0].Status = types.NodeStatusRetired },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			gs, err := n.Keeper.ExportGenesis(n.Ctx)
			require.NoError(t, err)
			mutate(gs)
			require.Error(t, gs.Validate())
		})
	}
}

func TestInvariants_aNameDepositMissingFromTheModuleAccountBreaksTheBalance(t *testing.T) {
	n := newNameFixture(t)
	require.NoError(t, n.claim(t, "node-1", "alpha"))
	n.Bank.balances[types.ModuleName] = n.Bank.balanceOf(types.ModuleName).Sub(n.deposit())

	got, err := n.Keeper.CheckInvariants(n.Ctx)

	require.NoError(t, err)
	require.False(t, got.BalanceMatches, "the ledger counts the name deposit, so a short module account is a broken invariant")
}

func TestNodeNames_aPageIsBoundedAndPagesByKey(t *testing.T) {
	f := newTestFixture(t)
	q := keeper.NewQueryServerImpl(f.Keeper)

	_, err := q.NodeNames(f.Ctx, &types.QueryNodeNamesRequest{Pagination: &query.PageRequest{Offset: 5}})
	require.Error(t, err, "an offset skips keys one by one, so the query refuses it")

	res, err := q.NodeNames(f.Ctx, &types.QueryNodeNamesRequest{Pagination: &query.PageRequest{Limit: 10 * keeper.MaxNodeNamesPerPage, CountTotal: true}})
	require.NoError(t, err)
	require.Zero(t, res.Pagination.Total, "a total would walk every name, so it is not computed")
}
