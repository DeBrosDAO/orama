package keeper_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/nodes/types"
)

func TestRegisterNode_literalIPBelongsToOneNode(t *testing.T) {
	f := newTestFixture(t)
	storageNode(t, f, "a", []string{"https://203.0.113.10:443"}, 15169)

	opB, hotB := newAccount(t), newAccount(t)
	f.fund(opB, 100)
	f.registerOperator(t, opB)
	reg := func(id string, endpoints ...string) error {
		_, err := f.Msg.RegisterNode(f.Ctx, &types.MsgRegisterNode{
			Operator: opB.String(), NodeId: id, Roles: []types.Role{types.RoleRelay}, HotKey: hotB.String(),
			Bindings:  withHot(t, opB, hotB, edBinding(t, testChainID, opB.String(), "tor")),
			Endpoints: endpoints,
		})
		return err
	}
	require.ErrorIs(t, reg("b1", "https://203.0.113.10:443"), types.ErrEndpointTaken)
	require.ErrorIs(t, reg("b2", "/ip4/203.0.113.10/tcp/4001"), types.ErrEndpointTaken, "the same address in another spelling")
	require.ErrorIs(t, reg("b3", "https://node.example:443", "203.0.113.10:8080"), types.ErrEndpointTaken, "one taken address among others")
	require.NoError(t, reg("b4", "https://node.example:443"), "hostnames are not indexed and may repeat")
}

func TestUpdateNode_endpointAddressesFollowTheNode(t *testing.T) {
	f := newTestFixture(t)
	opA, _ := storageNode(t, f, "a", []string{"https://203.0.113.10:443"}, 0)
	opB, _ := storageNode(t, f, "b", []string{"https://198.51.100.7:443"}, 0)
	setEndpoints := func(op sdk.AccAddress, id string, endpoints ...string) error {
		_, err := f.Msg.UpdateNode(f.Ctx, &types.MsgUpdateNode{Operator: op.String(), NodeId: id, SetEndpoints: true, Endpoints: endpoints})
		return err
	}

	require.ErrorIs(t, setEndpoints(opB, "b", "https://203.0.113.10:443"), types.ErrEndpointTaken)
	require.NoError(t, setEndpoints(opA, "a", "https://203.0.113.10:443", "https://192.0.2.9:443"), "a node may keep and add addresses")
	require.ErrorIs(t, setEndpoints(opB, "b", "https://192.0.2.9:443"), types.ErrEndpointTaken)

	require.NoError(t, setEndpoints(opA, "a", "https://node.example:443"), "moving off an address releases it")
	require.NoError(t, setEndpoints(opB, "b", "https://203.0.113.10:443"))

	_, err := f.Msg.RetireNode(f.Ctx, &types.MsgRetireNode{Operator: opB.String(), NodeId: "b"})
	require.NoError(t, err)
	require.NoError(t, setEndpoints(opA, "a", "https://203.0.113.10:443"), "retiring a node releases its addresses")
}

func TestGenesis_endpointAddressIndexIsRebuilt(t *testing.T) {
	f := newTestFixture(t)
	storageNode(t, f, "a", []string{"https://203.0.113.10:443"}, 15169)
	exported, err := f.Keeper.ExportGenesis(f.Ctx)
	require.NoError(t, err)

	dup := *exported
	dup.Nodes = append([]types.Node(nil), exported.Nodes...)
	clone := exported.Nodes[0]
	clone.NodeId = "b"
	cloneHot := newAccount(t)
	clone.HotKey = cloneHot.String()
	clone.Bindings = withHot(t, sdk.MustAccAddressFromBech32(clone.Operator), cloneHot, edBinding(t, testChainID, clone.Operator, "tor"))
	dup.Nodes = append(dup.Nodes, clone)
	require.ErrorIs(t, dup.Validate(), types.ErrEndpointTaken, "genesis cannot carry two live nodes on one address")

	g := newRawFixture(t)
	require.NoError(t, g.Keeper.InitGenesis(g.Ctx, *exported))
	op, hot := newAccount(t), newAccount(t)
	g.fund(op, 100)
	g.registerOperator(t, op)
	_, err = g.Msg.RegisterNode(g.Ctx, &types.MsgRegisterNode{
		Operator: op.String(), NodeId: "x", Roles: []types.Role{types.RoleRelay}, HotKey: hot.String(),
		Bindings:  withHot(t, op, hot, edBinding(t, testChainID, op.String(), "tor")),
		Endpoints: []string{"https://203.0.113.10:443"},
	})
	require.ErrorIs(t, err, types.ErrEndpointTaken, "an imported node still owns its address")
}

func lockedFixture(t *testing.T, lock int64) *testFixture {
	t.Helper()
	f := newTestFixture(t)
	p, err := f.Keeper.Params.Get(f.Ctx)
	require.NoError(t, err)
	p.NetworkIdentityLockSeconds = lock
	require.NoError(t, f.Keeper.Params.Set(f.Ctx, p))
	return f
}

func (f *testFixture) advance(d time.Duration) {
	f.Ctx = f.Ctx.WithBlockTime(f.Ctx.BlockTime().Add(d))
}

func TestNodeNetwork_identityCountsOnlyAfterTheLock(t *testing.T) {
	const lock = 1000
	f := lockedFixture(t, lock)
	op, _ := storageNode(t, f, "n", []string{"https://203.0.113.10:443"}, 15169)

	net, asn, err := f.Keeper.NodeNetwork(f.Ctx, "n")
	require.NoError(t, err)
	require.Empty(t, net, "a new node's identity is inside its lock")
	require.Zero(t, asn)

	f.advance((lock - 1) * time.Second)
	net, asn, _ = f.Keeper.NodeNetwork(f.Ctx, "n")
	require.Empty(t, net)
	require.Zero(t, asn)

	f.advance(time.Second)
	net, asn, err = f.Keeper.NodeNetwork(f.Ctx, "n")
	require.NoError(t, err)
	require.Equal(t, "203.0.0.0/16", net)
	require.Equal(t, uint32(15169), asn)

	// A change to the ASN starts the clock again, and so does one to the derived /16.
	_, err = f.Msg.UpdateNode(f.Ctx, &types.MsgUpdateNode{Operator: op.String(), NodeId: "n", SetAsn: true, Asn: 13335})
	require.NoError(t, err)
	net, asn, _ = f.Keeper.NodeNetwork(f.Ctx, "n")
	require.Empty(t, net)
	require.Zero(t, asn, "a changed ASN is not in effect yet")
	f.advance(lock * time.Second)
	net, asn, _ = f.Keeper.NodeNetwork(f.Ctx, "n")
	require.Equal(t, "203.0.0.0/16", net)
	require.Equal(t, uint32(13335), asn)

	_, err = f.Msg.UpdateNode(f.Ctx, &types.MsgUpdateNode{
		Operator: op.String(), NodeId: "n", SetEndpoints: true, Endpoints: []string{"https://198.51.100.7:443"},
	})
	require.NoError(t, err)
	net, _, _ = f.Keeper.NodeNetwork(f.Ctx, "n")
	require.Empty(t, net, "a moved /16 is not in effect yet")
}

func TestUpdateNode_changesThatLeaveTheIdentityAloneDoNotRestartTheLock(t *testing.T) {
	const lock = 1000
	f := lockedFixture(t, lock)
	op, _ := storageNode(t, f, "n", []string{"https://203.0.113.10:443"}, 15169)
	f.advance(lock * time.Second)

	_, err := f.Msg.UpdateNode(f.Ctx, &types.MsgUpdateNode{Operator: op.String(), NodeId: "n", SetRegionHint: true, RegionHint: "eu-2"})
	require.NoError(t, err)
	_, err = f.Msg.UpdateNode(f.Ctx, &types.MsgUpdateNode{Operator: op.String(), NodeId: "n", SetAsn: true, Asn: 15169})
	require.NoError(t, err, "declaring the same ASN again changes nothing")
	_, err = f.Msg.UpdateNode(f.Ctx, &types.MsgUpdateNode{
		Operator: op.String(), NodeId: "n", SetEndpoints: true, Endpoints: []string{"https://203.0.113.99:443"},
	})
	require.NoError(t, err, "another address in the same /16 is the same network")

	net, asn, err := f.Keeper.NodeNetwork(f.Ctx, "n")
	require.NoError(t, err)
	require.Equal(t, "203.0.0.0/16", net)
	require.Equal(t, uint32(15169), asn)
}

func TestNodeNetwork_lockZeroIsOff(t *testing.T) {
	f := lockedFixture(t, 0)
	storageNode(t, f, "n", []string{"https://203.0.113.10:443"}, 15169)
	net, asn, err := f.Keeper.NodeNetwork(f.Ctx, "n")
	require.NoError(t, err)
	require.Equal(t, "203.0.0.0/16", net)
	require.Equal(t, uint32(15169), asn)
}

func TestParams_networkIdentityLockMustNotBeNegative(t *testing.T) {
	p := types.DefaultParams()
	require.Equal(t, types.DefaultNetworkIdentityLockSeconds, p.NetworkIdentityLockSeconds)
	require.NoError(t, p.Validate())
	p.NetworkIdentityLockSeconds = -1
	require.Error(t, p.Validate())
}

func TestGenesis_identitySinceSurvivesRoundTrip(t *testing.T) {
	f := lockedFixture(t, 1000)
	storageNode(t, f, "n", []string{"https://203.0.113.10:443"}, 15169)
	exported, err := f.Keeper.ExportGenesis(f.Ctx)
	require.NoError(t, err)
	require.Equal(t, f.Ctx.BlockTime().Unix(), exported.Nodes[0].IdentitySinceUnix)

	g := newRawFixture(t)
	require.NoError(t, g.Keeper.InitGenesis(g.Ctx, *exported))
	net, _, err := g.Keeper.NodeNetwork(g.Ctx, "n")
	require.NoError(t, err)
	require.Empty(t, net, "an imported node keeps its lock clock")

	bad := *exported
	bad.Nodes = append([]types.Node(nil), exported.Nodes...)
	bad.Nodes[0].IdentitySinceUnix = -1
	require.Error(t, bad.Validate())
}

// requireIdentityIndexes checks that HotKeys and LiveIPs are exactly what the live nodes derive.
func requireIdentityIndexes(t *testing.T, f *testFixture) {
	t.Helper()
	wantHot, wantIP := map[string]string{}, map[string]string{}
	require.NoError(t, f.Keeper.Nodes.Walk(f.Ctx, nil, func(_ string, n types.Node) (bool, error) {
		if n.Status == types.NodeStatusRetired || n.Status == types.NodeStatusTombstoned {
			return false, nil
		}
		wantHot[n.HotKey] = n.NodeId
		for _, ip := range types.LiteralIPs(n.Endpoints) {
			wantIP[ip] = n.NodeId
		}
		return false, nil
	}))
	gotHot, gotIP := map[string]string{}, map[string]string{}
	require.NoError(t, f.Keeper.HotKeys.Walk(f.Ctx, nil, func(k, v string) (bool, error) { gotHot[k] = v; return false, nil }))
	require.NoError(t, f.Keeper.LiveIPs.Walk(f.Ctx, nil, func(k, v string) (bool, error) { gotIP[k] = v; return false, nil }))
	require.Equal(t, wantHot, gotHot)
	require.Equal(t, wantIP, gotIP)
}

func TestIdentityIndexes_stayInLineThroughTheLifecycle(t *testing.T) {
	f := newTestFixture(t)
	opA, hotA := storageNode(t, f, "a", []string{"https://203.0.113.10:443", "/ip4/198.51.100.7/tcp/4001"}, 0)
	storageNode(t, f, "b", []string{"https://192.0.2.9:443"}, 0)
	requireIdentityIndexes(t, f)

	next := newAccount(t)
	_, err := f.Msg.UpdateNode(f.Ctx, &types.MsgUpdateNode{
		Operator: opA.String(), NodeId: "a", HotKey: next.String(),
		Bindings:     withHot(t, opA, next, secpBinding(t, testChainID, opA.String(), "hot")),
		SetEndpoints: true, Endpoints: []string{"https://203.0.113.10:443"},
	})
	require.NoError(t, err)
	requireIdentityIndexes(t, f)
	_, isHot := f.Keeper.HotKeys.Get(f.Ctx, hotA.String())
	require.Error(t, isHot, "the replaced hot key is released")

	_, err = f.Msg.RetireNode(f.Ctx, &types.MsgRetireNode{Operator: opA.String(), NodeId: "a"})
	require.NoError(t, err)
	requireIdentityIndexes(t, f)
	require.NoError(t, f.Keeper.Tombstone(f.Ctx, "b"))
	requireIdentityIndexes(t, f)
}
