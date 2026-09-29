package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/nodes/types"
)

// storageNode registers an operator with one STORAGE node and returns the accounts.
func storageNode(t *testing.T, f *testFixture, id string, endpoints []string, asn uint32) (op, hot sdk.AccAddress) {
	t.Helper()
	op, hot = newAccount(t), newAccount(t)
	f.fund(op, 100)
	f.registerOperator(t, op)
	_, err := f.Msg.RegisterNode(f.Ctx, &types.MsgRegisterNode{
		Operator:  op.String(),
		NodeId:    id,
		Roles:     []types.Role{types.RoleStorage},
		HotKey:    hot.String(),
		Bindings:  []types.Binding{secpBinding(t, testChainID, op.String(), "hot")},
		Endpoints: endpoints,
		Asn:       asn,
	})
	require.NoError(t, err)
	return op, hot
}

func TestFundHotKey_movesEarningsToOwnNodesHotKey(t *testing.T) {
	f := newTestFixture(t)
	op, hot := storageNode(t, f, "node-1", []string{"https://203.0.113.10:443"}, 64495)
	f.Earnings.balances[op.String()] = math.NewInt(1_000)

	_, err := f.Msg.FundHotKey(f.Ctx, &types.MsgFundHotKey{Operator: op.String(), NodeId: "node-1", Amount: math.NewInt(400)})
	require.NoError(t, err)

	require.True(t, f.Earnings.balanceOf(op).Equal(math.NewInt(600)))
	require.True(t, f.Earnings.balanceOf(hot).Equal(math.NewInt(400)))
}

func TestFundHotKey_refusesAnotherOperatorsNode(t *testing.T) {
	f := newTestFixture(t)
	_, hot := storageNode(t, f, "node-1", nil, 0)
	thief := newAccount(t)
	f.registerOperator(t, thief)
	f.Earnings.balances[thief.String()] = math.NewInt(1_000)

	_, err := f.Msg.FundHotKey(f.Ctx, &types.MsgFundHotKey{Operator: thief.String(), NodeId: "node-1", Amount: math.NewInt(1)})
	require.ErrorIs(t, err, types.ErrUnauthorized)
	require.True(t, f.Earnings.balanceOf(hot).IsZero())
	require.True(t, f.Earnings.balanceOf(thief).Equal(math.NewInt(1_000)))
}

func TestFundHotKey_refusesUnknownNodeOverdraftZeroAndRetired(t *testing.T) {
	f := newTestFixture(t)
	op, hot := storageNode(t, f, "node-1", nil, 0)
	f.Earnings.balances[op.String()] = math.NewInt(100)

	_, err := f.Msg.FundHotKey(f.Ctx, &types.MsgFundHotKey{Operator: op.String(), NodeId: "ghost", Amount: math.NewInt(1)})
	require.ErrorIs(t, err, types.ErrNotFound)
	_, err = f.Msg.FundHotKey(f.Ctx, &types.MsgFundHotKey{Operator: op.String(), NodeId: "node-1", Amount: math.NewInt(101)})
	require.Error(t, err)
	_, err = f.Msg.FundHotKey(f.Ctx, &types.MsgFundHotKey{Operator: op.String(), NodeId: "node-1", Amount: math.ZeroInt()})
	require.Error(t, err)

	_, err = f.Msg.RetireNode(f.Ctx, &types.MsgRetireNode{Operator: op.String(), NodeId: "node-1"})
	require.NoError(t, err)
	_, err = f.Msg.FundHotKey(f.Ctx, &types.MsgFundHotKey{Operator: op.String(), NodeId: "node-1", Amount: math.NewInt(1)})
	require.Error(t, err)

	require.True(t, f.Earnings.balanceOf(op).Equal(math.NewInt(100)))
	require.True(t, f.Earnings.balanceOf(hot).IsZero())
}

func TestFundHotKey_followsRotatedHotKey(t *testing.T) {
	f := newTestFixture(t)
	op, oldHot := storageNode(t, f, "node-1", nil, 0)
	newHot := newAccount(t)
	_, err := f.Msg.UpdateNode(f.Ctx, &types.MsgUpdateNode{Operator: op.String(), NodeId: "node-1", HotKey: newHot.String()})
	require.NoError(t, err)
	f.Earnings.balances[op.String()] = math.NewInt(50)

	_, err = f.Msg.FundHotKey(f.Ctx, &types.MsgFundHotKey{Operator: op.String(), NodeId: "node-1", Amount: math.NewInt(50)})
	require.NoError(t, err)
	require.True(t, f.Earnings.balanceOf(newHot).Equal(math.NewInt(50)))
	require.True(t, f.Earnings.balanceOf(oldHot).IsZero())
}

func TestNodeNetwork_derivedFromEndpointsAndDeclaredAsn(t *testing.T) {
	f := newTestFixture(t)
	storageNode(t, f, "node-1", []string{"https://node.example:443", "https://203.0.113.10:443"}, 64495)
	storageNode(t, f, "node-2", []string{"https://node.example:443"}, 0)

	net, asn, err := f.Keeper.NodeNetwork(f.Ctx, "node-1")
	require.NoError(t, err)
	require.Equal(t, "203.0.0.0/16", net)
	require.Equal(t, uint32(64495), asn)

	net, asn, err = f.Keeper.NodeNetwork(f.Ctx, "node-2")
	require.NoError(t, err)
	require.Empty(t, net, "a hostname-only node has no derivable network")
	require.Zero(t, asn)

	_, _, err = f.Keeper.NodeNetwork(f.Ctx, "ghost")
	require.ErrorIs(t, err, types.ErrNotFound)
}

func TestUpdateNode_setsAndClearsAsnAndEndpoints(t *testing.T) {
	f := newTestFixture(t)
	op, _ := storageNode(t, f, "node-1", nil, 0)

	_, err := f.Msg.UpdateNode(f.Ctx, &types.MsgUpdateNode{Operator: op.String(), NodeId: "node-1", SetAsn: true, Asn: 15169})
	require.NoError(t, err)
	_, err = f.Msg.UpdateNode(f.Ctx, &types.MsgUpdateNode{
		Operator: op.String(), NodeId: "node-1", SetEndpoints: true, Endpoints: []string{"https://198.51.100.7:443"},
	})
	require.NoError(t, err)
	net, asn, err := f.Keeper.NodeNetwork(f.Ctx, "node-1")
	require.NoError(t, err)
	require.Equal(t, "198.51.0.0/16", net)
	require.Equal(t, uint32(15169), asn)

	_, err = f.Msg.UpdateNode(f.Ctx, &types.MsgUpdateNode{Operator: op.String(), NodeId: "node-1", SetAsn: true})
	require.NoError(t, err)
	_, asn, err = f.Keeper.NodeNetwork(f.Ctx, "node-1")
	require.NoError(t, err)
	require.Zero(t, asn, "set_asn with 0 clears the declaration")

	_, err = f.Msg.UpdateNode(f.Ctx, &types.MsgUpdateNode{Operator: op.String(), NodeId: "node-1", SetAsn: true, Asn: 64512})
	require.Error(t, err, "a private-use asn is refused")
}

func TestRegisterNode_refusesReservedAsn(t *testing.T) {
	f := newTestFixture(t)
	op, hot := newAccount(t), newAccount(t)
	f.registerOperator(t, op)
	_, err := f.Msg.RegisterNode(f.Ctx, &types.MsgRegisterNode{
		Operator: op.String(), NodeId: "n", Roles: []types.Role{types.RoleStorage}, HotKey: hot.String(),
		Bindings: []types.Binding{secpBinding(t, testChainID, op.String(), "hot")}, Asn: 23456,
	})
	require.Error(t, err)
}

func TestStorageChanges_queueTracksEveryStorageNodeWrite(t *testing.T) {
	f := newTestFixture(t)
	op, hot := newAccount(t), newAccount(t)
	f.fund(op, 100)
	f.registerOperator(t, op)
	relayOnly := newAccount(t)
	f.fund(relayOnly, 100)
	f.registerOperator(t, relayOnly)
	f.registerNode(t, relayOnly, hot, "relay-1", []types.Role{types.RoleRelay}, []types.Binding{secpBinding(t, testChainID, relayOnly.String(), "hot")})
	f.registerNode(t, op, newAccount(t), "store-1", []types.Role{types.RoleStorage}, []types.Binding{secpBinding(t, testChainID, op.String(), "hot")})

	ids, err := f.Keeper.TakeStorageChanges(f.Ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"store-1"}, ids, "only STORAGE-role nodes are queued")
	ids, err = f.Keeper.TakeStorageChanges(f.Ctx)
	require.NoError(t, err)
	require.Empty(t, ids, "the queue is cleared by a take")

	ok, err := f.Keeper.StorageEligible(f.Ctx, "store-1")
	require.NoError(t, err)
	require.False(t, ok, "registered but unbonded")

	_, err = f.Msg.BondNode(f.Ctx, &types.MsgBondNode{Operator: op.String(), NodeId: "store-1", Role: types.RoleStorage, Amount: orama(1)})
	require.NoError(t, err)
	ids, err = f.Keeper.TakeStorageChanges(f.Ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"store-1"}, ids)
	ok, err = f.Keeper.StorageEligible(f.Ctx, "store-1")
	require.NoError(t, err)
	require.True(t, ok)

	require.NoError(t, f.Keeper.Jail(f.Ctx, "store-1"))
	ids, err = f.Keeper.TakeStorageChanges(f.Ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"store-1"}, ids)
	ok, err = f.Keeper.StorageEligible(f.Ctx, "store-1")
	require.NoError(t, err)
	require.False(t, ok, "a jailed node is not eligible")

	ok, err = f.Keeper.StorageEligible(f.Ctx, "ghost")
	require.NoError(t, err)
	require.False(t, ok)
}

func TestStorageEligible_falseWhenStorageRoleBelowMinEvenIfOtherRoleActive(t *testing.T) {
	f := newTestFixture(t)
	op, hot := newAccount(t), newAccount(t)
	f.fund(op, 100)
	f.registerOperator(t, op)
	f.registerNode(t, op, hot, "n", []types.Role{types.RoleStorage, types.RoleRelay}, []types.Binding{secpBinding(t, testChainID, op.String(), "hot")})
	_, err := f.Msg.BondNode(f.Ctx, &types.MsgBondNode{Operator: op.String(), NodeId: "n", Role: types.RoleRelay, Amount: orama(1)})
	require.NoError(t, err)

	active, err := f.Keeper.IsActive(f.Ctx, "n")
	require.NoError(t, err)
	require.True(t, active)
	ok, err := f.Keeper.StorageEligible(f.Ctx, "n")
	require.NoError(t, err)
	require.False(t, ok, "an active RELAY role does not make the STORAGE role eligible")
}

func TestNetworkOf(t *testing.T) {
	cases := map[string]struct {
		endpoints []string
		want      string
	}{
		"nil":                {nil, ""},
		"hostname only":      {[]string{"https://node.example:443"}, ""},
		"v4 url":             {[]string{"https://203.0.113.10:443"}, "203.0.0.0/16"},
		"v4 host:port":       {[]string{"198.51.100.7:4001"}, "198.51.0.0/16"},
		"v4 multiaddr":       {[]string{"/ip4/192.0.2.9/tcp/4001"}, "192.0.0.0/16"},
		"first literal wins": {[]string{"https://a.example", "https://198.51.100.7", "https://203.0.113.10"}, "198.51.0.0/16"},
		"v6 groups by /32":   {[]string{"https://[2001:db8:1:2::1]:443"}, "2001:db8::/32"},
		"onion is not an ip": {[]string{"/onion3/abcdefghijklmnop:80"}, ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tc.want, types.NetworkOf(tc.endpoints))
		})
	}
}

func TestValidateASN(t *testing.T) {
	for _, bad := range []uint32{0, 23456, 64496, 64511, 64512, 65534, 65535, 65536, 65551, 4200000000, 4294967295} {
		require.Error(t, types.ValidateASN(bad), "asn %d", bad)
	}
	for _, good := range []uint32{1, 15169, 64495, 65552, 4199999999} {
		require.NoError(t, types.ValidateASN(good), "asn %d", good)
	}
}

func TestGenesis_roundTripKeepsAsnAndQueuesStorageNodes(t *testing.T) {
	f := newTestFixture(t)
	op, _ := storageNode(t, f, "node-1", []string{"https://203.0.113.10:443"}, 15169)
	_, err := f.Msg.BondNode(f.Ctx, &types.MsgBondNode{Operator: op.String(), NodeId: "node-1", Role: types.RoleStorage, Amount: orama(1)})
	require.NoError(t, err)
	_, err = f.Keeper.TakeStorageChanges(f.Ctx)
	require.NoError(t, err)

	exported, err := f.Keeper.ExportGenesis(f.Ctx)
	require.NoError(t, err)

	g := newRawFixture(t)
	g.Bank.fund(types.ModuleName, orama(1))
	require.NoError(t, g.Keeper.InitGenesis(g.Ctx, *exported))

	net, asn, err := g.Keeper.NodeNetwork(g.Ctx, "node-1")
	require.NoError(t, err)
	require.Equal(t, "203.0.0.0/16", net)
	require.Equal(t, uint32(15169), asn)
	ids, err := g.Keeper.TakeStorageChanges(g.Ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"node-1"}, ids, "an imported STORAGE node is queued so x/storage tracks it in the first block")
}

func TestGenesis_refusesReservedAsn(t *testing.T) {
	f := newTestFixture(t)
	storageNode(t, f, "node-1", nil, 15169)
	exported, err := f.Keeper.ExportGenesis(f.Ctx)
	require.NoError(t, err)
	exported.Nodes[0].Asn = 64512
	require.Error(t, exported.Validate())
}
