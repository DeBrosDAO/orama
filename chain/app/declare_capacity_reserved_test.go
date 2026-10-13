package app_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	nodeskeeper "github.com/DeBrosOfficial/network/chain/x/nodes/keeper"
	nodestypes "github.com/DeBrosOfficial/network/chain/x/nodes/types"
)

// storageHolderChain returns a chain where s1 holds a replica that x/storage has reserved bytes for.
func storageHolderChain(t *testing.T) (*wiringChain, wiringNode) {
	t.Helper()
	c := newWiringChain(t)
	s1 := c.addStorageNode("s1", "https://45.33.100.10:443", 15169)
	c.addStorageNode("s2", "https://93.184.113.10:443", 13335)
	c.addStorageNode("s3", "https://151.101.2.10:443", 16509)
	c.blocks(1)
	dealID := c.privateDeal()
	c.blocks(1)
	require.ElementsMatch(t, []string{"s1", "s2", "s3"}, c.slotHolders(dealID))
	node, err := c.app.NodesKeeper.GetNode(c.app.NewContext(true), "s1")
	require.NoError(t, err)
	require.NotZero(t, node.ReservedCapacityBytes, "s1 holds a replica, so its bytes are reserved on x/nodes")
	require.NoError(t, c.app.CheckConservationFull(c.app.NewContext(true)), "the chain is conserved before the declaration")
	return c, s1
}

// x/nodes owns the reservation: x/storage reserves a node's bytes there as it binds a replica, so
// MsgDeclareCapacity's "declared < reserved" guard reads the bytes the node really holds.
func TestDeclareCapacityRefusesLessThanStorageReserved(t *testing.T) {
	c, s1 := storageHolderChain(t)
	var declareErr error
	c.write(func(ctx sdk.Context) {
		_, declareErr = nodeskeeper.NewMsgServerImpl(c.app.NodesKeeper).DeclareCapacity(ctx, &nodestypes.MsgDeclareCapacity{
			Operator: s1.operator.String(), NodeId: "s1", CapacityBytes: 0,
		})
	})
	require.Error(t, declareErr, "an operator must not declare less capacity than x/storage has reserved on the node")
}

// The consequence: x/storage's "reserved within declared" invariant is part of the in-block
// conservation full pass (EndBlocker returns its error, which halts the chain).
func TestDeclaringLessThanReservedKeepsConservation(t *testing.T) {
	c, s1 := storageHolderChain(t)
	c.write(func(ctx sdk.Context) {
		// The declaration succeeds on HEAD; the test is about what it leaves behind.
		_, _ = nodeskeeper.NewMsgServerImpl(c.app.NodesKeeper).DeclareCapacity(ctx, &nodestypes.MsgDeclareCapacity{
			Operator: s1.operator.String(), NodeId: "s1", CapacityBytes: 0,
		})
	})
	require.NoError(t, c.app.CheckConservationFull(c.app.NewContext(true)),
		"the conservation full pass (it halts the chain when it fails) must still hold")
}

// The next block must still be producible (on a test chain the full pass runs every block; in
// production at every multiple of emission min_blocks_per_epoch).
func TestBlockAfterDeclaringLessThanReservedStillCommits(t *testing.T) {
	c, s1 := storageHolderChain(t)
	c.write(func(ctx sdk.Context) {
		_, _ = nodeskeeper.NewMsgServerImpl(c.app.NodesKeeper).DeclareCapacity(ctx, &nodestypes.MsgDeclareCapacity{
			Operator: s1.operator.String(), NodeId: "s1", CapacityBytes: 0,
		})
	})
	require.NotPanics(t, func() { c.blocks(1) })
}

// Same root cause through MsgRetireNode: its "node still has reserved bytes" guard reads the same
// always-zero x/nodes counter, so a node that holds a replica retires and its declared capacity is
// zeroed under x/storage's reservation.
func TestRetireNodeRefusedWhileStorageHoldsItsReplicas(t *testing.T) {
	c, s1 := storageHolderChain(t)
	var retireErr error
	c.write(func(ctx sdk.Context) {
		_, retireErr = nodeskeeper.NewMsgServerImpl(c.app.NodesKeeper).RetireNode(ctx, &nodestypes.MsgRetireNode{
			Operator: s1.operator.String(), NodeId: "s1",
		})
	})
	require.Error(t, retireErr, "a node that still holds a replica must not retire")
}

func TestBlockAfterRetiringAHolderStillCommits(t *testing.T) {
	c, s1 := storageHolderChain(t)
	c.write(func(ctx sdk.Context) {
		_, _ = nodeskeeper.NewMsgServerImpl(c.app.NodesKeeper).RetireNode(ctx, &nodestypes.MsgRetireNode{
			Operator: s1.operator.String(), NodeId: "s1",
		})
	})
	c.blocks(1)
}
