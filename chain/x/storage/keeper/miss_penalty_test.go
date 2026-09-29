package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/storage/keeper"
	"github.com/DeBrosOfficial/network/chain/x/storage/types"
)

// twoFullDeals opens two PRIVATE deals of three 1 KiB replicas on three nodes that each declare
// room for exactly those two replicas, and accepts every slot.
func twoFullDeals(t *testing.T) (*fixture, []*nodeInfo, []uint64) {
	t.Helper()
	f := newFixture(t)
	f.init(t, func(gs *types.GenesisState) {
		gs.Params.MissThreshold = 4
		gs.Params.SMinProviders = 8
	})
	nodes := f.threeNodes(t, 2048)
	client := acc(9)
	f.fund(client, 1_000_000)
	pieces := []types.PieceCommitment{commit(t, payload(1)), commit(t, payload(2)), commit(t, payload(3))}
	id1 := f.createDeal(t, types.DealClass_DEAL_CLASS_PRIVATE, client, "", 3, 1000, 20, pieces)
	id2 := f.createDeal(t, types.DealClass_DEAL_CLASS_PRIVATE, client, "", 3, 1000, 20, pieces)
	f.end(t)
	f.begin(t)
	f.acceptAll(t, id1, 3)
	f.acceptAll(t, id2, 3)
	return f, nodes, []uint64{id1, id2}
}

// missEpochs closes n epochs in which nobody proves anything.
func (f *fixture) missEpochs(t *testing.T, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		f.end(t)
		f.Emission.epoch++
		f.begin(t)
	}
	f.end(t)
}

// holders records who holds every slot of the deals.
func (f *fixture) holders(t *testing.T, ids []uint64) map[[2]uint64]string {
	t.Helper()
	out := map[[2]uint64]string{}
	for _, id := range ids {
		for i := uint32(0); i < 3; i++ {
			out[[2]uint64{id, uint64(i)}] = f.slot(t, id, i).NodeId
		}
	}
	return out
}

// requireEvicted requires that no slot is still held by the node that held it in before. Once a
// slot is evicted the deal can hand it to another operator, so it may well be held again by
// someone else.
func (f *fixture) requireEvicted(t *testing.T, before map[[2]uint64]string) {
	t.Helper()
	for key, holder := range before {
		if holder == "" {
			continue
		}
		require.NotEqual(t, holder, f.slot(t, key[0], uint32(key[1])).NodeId, "deal %d slot %d is still held by its silent node", key[0], key[1])
	}
}

// A node whose slash fails (x/nodes refuses it, whatever the reason) is still evicted from the slot
// it never served: the miss and the eviction do not depend on the penalty. Before, the slash error
// rolled the whole row back, so the node kept the slot forever.
func TestMiss_aFailingSlashNeverBlocksEviction(t *testing.T) {
	f, nodes, ids := twoFullDeals(t)
	for _, n := range nodes {
		n.slashErr = errf("slash would drop backed capacity below reserved")
	}
	before := f.holders(t, ids)

	f.missEpochs(t, 6)

	f.requireEvicted(t, before)
	for _, n := range nodes {
		require.Zero(t, n.slashN)
	}
	count, err := f.Keeper.FailureCount(f.Ctx, nodes[0].id, keeper.FailureKindSlash)
	require.NoError(t, err)
	require.NotZero(t, count, "the refused slash is reported against the node")
	require.Contains(t, eventTypes(f.Ctx), "storage_slot_evicted")
	f.requireInvariants(t)
}

// A full node's slash lowers its declared capacity to what the smaller bond backs. The replicas
// that no longer fit are released, so reserved never exceeds declared, and the misses still evict.
func TestMiss_slashingAFullNodeReleasesTheReplicasItCanNoLongerHold(t *testing.T) {
	f, nodes, ids := twoFullDeals(t)
	for _, n := range nodes {
		n.capacityAfterSlash = 1024
	}

	// Two consecutive misses slash; stop before the miss threshold evicts anything by itself.
	f.missEpochs(t, 3)

	held := 0
	for _, id := range ids {
		for i := uint32(0); i < 3; i++ {
			if f.slot(t, id, i).NodeId != "" {
				held++
			}
		}
	}
	require.Equal(t, 3, held, "each node keeps exactly the one replica its clamped declaration holds")
	for _, n := range nodes {
		require.GreaterOrEqual(t, n.slashN, 1)
		require.Equal(t, uint64(1024), n.capacity)
	}
	f.requireInvariants(t)

	before := f.holders(t, ids)
	f.missEpochs(t, 4)
	f.requireEvicted(t, before)
	f.requireInvariants(t)
}

// A probation node has no bond. Its record deposit is its stake: misses slash it, the node is
// evicted at the threshold, and a deposit burned in full is no longer recorded as held.
func TestMiss_probationNodeLosesItsDepositAndItsSlot(t *testing.T) {
	f, nodes, id, data := probationFixture(t)
	f.nextEpoch(t)
	f.proveAll(t, id, data)
	f.nextEpoch(t)
	held := f.slot(t, id, 0)
	depositID := probationDepositKeyPrefix + held.NodeId
	require.Equal(t, math.NewInt(900), f.Deposits.locked[depositID])
	burnedBefore := f.Bank.burned
	before := f.holders(t, []uint64{id})

	for i := 0; i < 5; i++ {
		f.nextEpoch(t)
	}

	f.requireEvicted(t, before)
	require.Less(t, f.Deposits.locked[depositID].Int64(), int64(900), "the misses burned part of the deposit")
	require.True(t, f.Bank.burned.GT(burnedBefore))
	require.Contains(t, eventTypes(f.Ctx), "storage_probation_slashed")
	n, err := f.Keeper.FailureCount(f.Ctx, held.NodeId, keeper.FailureKindSlash)
	require.NoError(t, err)
	require.Zero(t, n, "the deposit slash applied cleanly")
	for _, node := range nodes {
		require.Zero(t, node.slashN, "a probation node is never slashed through x/nodes")
	}
	f.requireInvariants(t)
}

// A deposit burned in full leaves no deposit behind for the node to be released or slashed again.
func TestMiss_aDepositBurnedInFullIsForgotten(t *testing.T) {
	f, _, id, data := probationFixture(t)
	f.nextEpoch(t)
	f.proveAll(t, id, data)
	f.nextEpoch(t)
	held := f.slot(t, id, 0)
	depositID := probationDepositKeyPrefix + held.NodeId
	before := f.holders(t, []uint64{id})
	f.Deposits.locked[depositID] = math.NewInt(50)
	require.NoError(t, f.Bank.BurnCoins(f.Ctx, "fees_deposits", sdk.NewCoins(sdk.NewCoin(params.BaseDenom, math.NewInt(850)))))

	for i := 0; i < 5; i++ {
		f.nextEpoch(t)
	}

	f.requireEvicted(t, before)
	_, open := f.Deposits.locked[depositID]
	require.False(t, open, "the deposit was burned in full and closed")
	state, err := f.Keeper.Nodes.Get(f.Ctx, held.NodeId)
	require.NoError(t, err)
	require.False(t, state.DepositLocked)
	f.requireInvariants(t)
}

// A probation node that has not earned a deposit yet has nothing to slash. Its misses still count
// and it is still evicted; before, the missing bond failed the row and it kept the slot forever.
func TestMiss_probationNodeWithoutADepositIsStillEvicted(t *testing.T) {
	f, nodes, id, _ := probationFixture(t)
	before := f.holders(t, []uint64{id})

	for i := 0; i < 6; i++ {
		f.nextEpoch(t)
	}

	f.requireEvicted(t, before)
	require.Empty(t, f.Deposits.locked)
	for _, n := range nodes {
		count, err := f.Keeper.FailureCount(f.Ctx, n.id, keeper.FailureKindSettlement)
		require.NoError(t, err)
		require.Zero(t, count)
	}
	require.Contains(t, eventTypes(f.Ctx), "storage_slot_evicted")
	f.requireInvariants(t)
}
