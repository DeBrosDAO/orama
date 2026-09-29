package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	"github.com/DeBrosOfficial/network/chain/x/storage/types"
)

// registerUntracked adds a node to x/nodes' side (the fake) exactly as x/nodes does on a write:
// the node exists and is queued, but x/storage has not reconciled it yet.
func (f *fixture) registerUntracked(id, net string, asn uint32, capacity uint64) *nodeInfo {
	f.seq++
	info := &nodeInfo{
		id: id, hot: acc(f.seq), operator: acc(f.seq + 100),
		net: net, asn: asn, capacity: capacity, active: true, slashed: math.ZeroInt(),
	}
	f.Nodes.byID[id] = info
	f.Nodes.touch(id)
	return info
}

func (f *fixture) tracked(t *testing.T, id string) bool {
	t.Helper()
	has, err := f.Keeper.Nodes.Has(f.Ctx, id)
	require.NoError(t, err)
	return has
}

func TestSyncNodes_bondedNodeBecomesTrackedAndAssignable(t *testing.T) {
	f := newFixture(t)
	f.init(t, nil)
	f.registerUntracked("n1", "10.0.0.0/16", 1, 1<<20)
	f.registerUntracked("n2", "10.1.0.0/16", 2, 1<<20)
	f.registerUntracked("n3", "10.2.0.0/16", 3, 1<<20)
	require.False(t, f.tracked(t, "n1"), "nothing is tracked before a block reconciles")

	client := acc(9)
	f.fund(client, 100_000)
	pieces := []types.PieceCommitment{commit(t, payload(1)), commit(t, payload(2)), commit(t, payload(3))}
	id := f.createDeal(t, types.DealClass_DEAL_CLASS_PRIVATE, client, "", 3, 1000, 1, pieces)
	f.end(t)
	f.begin(t)

	for _, n := range []string{"n1", "n2", "n3"} {
		require.True(t, f.tracked(t, n))
	}
	for i := uint32(0); i < 3; i++ {
		require.NotEmpty(t, f.slot(t, id, i).NodeId, "slot %d has a provider", i)
	}
	f.requireInvariants(t)
}

func TestSyncNodes_unbondedNodeIsNotTracked(t *testing.T) {
	f := newFixture(t)
	f.init(t, nil)
	info := f.registerUntracked("n1", "10.0.0.0/16", 1, 1<<20)
	info.active = false

	f.end(t)
	f.begin(t)
	require.False(t, f.tracked(t, "n1"))
}

func TestSyncNodes_untracksIdleNodeThatStopsQualifying(t *testing.T) {
	f := newFixture(t)
	f.init(t, nil)
	info := f.registerUntracked("n1", "10.0.0.0/16", 1, 1<<20)
	f.end(t)
	f.begin(t)
	require.True(t, f.tracked(t, "n1"))

	info.active = false
	f.Nodes.touch("n1")
	f.end(t)
	f.begin(t)
	require.False(t, f.tracked(t, "n1"))

	info.active = true
	f.Nodes.touch("n1")
	f.end(t)
	f.begin(t)
	require.True(t, f.tracked(t, "n1"), "a node that qualifies again is tracked again")
}

func TestSyncNodes_keepsStateWhileReplicasHeldThenDrops(t *testing.T) {
	f := newFixture(t)
	f.init(t, nil)
	f.registerUntracked("n1", "10.0.0.0/16", 1, 1<<20)
	f.registerUntracked("n2", "10.1.0.0/16", 2, 1<<20)
	f.registerUntracked("n3", "10.2.0.0/16", 3, 1<<20)
	client := acc(9)
	f.fund(client, 100_000)
	pieces := []types.PieceCommitment{commit(t, payload(1)), commit(t, payload(2)), commit(t, payload(3))}
	id := f.createDeal(t, types.DealClass_DEAL_CLASS_PRIVATE, client, "", 3, 1000, 1, pieces)
	f.end(t)
	f.begin(t)
	holder := f.slot(t, id, 0).NodeId
	require.NotEmpty(t, holder)

	f.Nodes.byID[holder].active = false
	f.Nodes.touch(holder)
	f.end(t)
	f.begin(t)
	require.True(t, f.tracked(t, holder), "a node holding replicas stays tracked")
	f.end(t)
	f.begin(t)
	require.True(t, f.tracked(t, holder), "and is retried, not forgotten")

	slot := f.slot(t, id, 0)
	f.Nodes.byID[holder].active = true
	_, err := f.Msg.ReleaseReplica(f.Ctx, &types.MsgReleaseReplica{
		Signer: f.Nodes.byID[holder].hot.String(), NodeId: holder, DealId: id, Slot: slot.Index,
		Reason: types.ReleaseReason_RELEASE_REASON_LEGAL,
	})
	require.NoError(t, err)
	f.Nodes.byID[holder].active = false
	f.end(t)
	f.begin(t)
	f.end(t)
	f.begin(t)
	require.False(t, f.tracked(t, holder), "once its last replica is gone the node is untracked")
}

func TestSyncNodes_doesNotResetTrackedState(t *testing.T) {
	f := newFixture(t)
	f.init(t, nil)
	f.registerUntracked("n1", "10.0.0.0/16", 1, 1<<20)
	f.end(t)
	f.begin(t)
	state, err := f.Keeper.Nodes.Get(f.Ctx, "n1")
	require.NoError(t, err)
	state.EverProved = true
	require.NoError(t, f.Keeper.Nodes.Set(f.Ctx, "n1", state))

	f.Nodes.touch("n1")
	f.end(t)
	f.begin(t)
	got, err := f.Keeper.Nodes.Get(f.Ctx, "n1")
	require.NoError(t, err)
	require.True(t, got.EverProved, "a repeat write to a tracked node keeps its storage state")
}

func TestProtocolSlots_refuseNodesWithoutNetworkOrASN(t *testing.T) {
	f := newFixture(t)
	f.init(t, nil)
	f.addNode(t, "a", "10.0.0.0/16", 1, 1<<20, false)
	f.addNode(t, "b", "10.1.0.0/16", 2, 1<<20, false)
	f.addNode(t, "no-net", "", 3, 1<<20, false)
	f.addNode(t, "no-asn", "10.2.0.0/16", 0, 1<<20, false)
	id, err := f.Keeper.CreateProtocolDeal(f.Ctx, types.DealClass_DEAL_CLASS_ARCHIVE, payload(5), math.NewInt(1000), 1)
	require.NoError(t, err)
	f.end(t)
	f.begin(t)
	require.Equal(t, types.DealStatus_DEAL_STATUS_REFUNDED, f.deal(t, id).Status,
		"two identified nodes cannot fill three diverse slots, and unidentified nodes do not count")
}

func TestPrivateDeals_acceptNodesWithoutNetworkIdentity(t *testing.T) {
	f := newFixture(t)
	f.init(t, nil)
	f.addNode(t, "a", "", 0, 1<<20, false)
	f.addNode(t, "b", "", 0, 1<<20, false)
	f.addNode(t, "c", "", 0, 1<<20, false)
	client := acc(9)
	f.fund(client, 100_000)
	pieces := []types.PieceCommitment{commit(t, payload(1)), commit(t, payload(2)), commit(t, payload(3))}
	id := f.createDeal(t, types.DealClass_DEAL_CLASS_PRIVATE, client, "", 3, 1000, 1, pieces)
	f.end(t)
	f.begin(t)
	for i := uint32(0); i < 3; i++ {
		require.NotEmpty(t, f.slot(t, id, i).NodeId, "the distinct-network rule applies to protocol deals only")
	}
}

func TestSyncNodes_oneBrokenNodeDoesNotHaltTheBlock(t *testing.T) {
	f := newFixture(t)
	f.init(t, nil)
	f.registerUntracked("good", "10.0.0.0/16", 1, 1<<20)
	f.Nodes.touch("ghost") // queued by x/nodes, but x/nodes cannot resolve it.

	require.NoError(t, f.Keeper.BeginBlock(f.Ctx), "a per-node inconsistency must not fail BeginBlock")
	require.True(t, f.tracked(t, "good"), "healthy nodes are still reconciled")
	require.False(t, f.tracked(t, "ghost"))
	require.Contains(t, f.Nodes.dirty, "ghost", "the broken node stays queued for the next block")

	var failed []string
	for _, ev := range f.Ctx.EventManager().Events() {
		if ev.Type == "storage_node_sync_failed" {
			for _, a := range ev.Attributes {
				if a.Key == "node_id" {
					failed = append(failed, a.Value)
				}
			}
		}
	}
	require.Equal(t, []string{"ghost"}, failed)

	f.registerUntracked("ghost", "10.1.0.0/16", 2, 1<<20)
	require.NoError(t, f.Keeper.BeginBlock(f.Ctx))
	require.True(t, f.tracked(t, "ghost"), "once the record is consistent the retry succeeds")
}
