package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DeBrosOfficial/network/chain/x/storage/types"
)

// exportedWithReplicas runs a three-replica deal to the point where every slot is bound to a node,
// and returns the fixture with the genesis it exports.
func exportedWithReplicas(t *testing.T) (*fixture, *types.GenesisState) {
	t.Helper()
	f := newFixture(t)
	f.init(t, nil)
	f.threeNodes(t, 1<<20)
	client := acc(9)
	f.fund(client, 100_000_000)
	data := payload(7)
	id := f.createDeal(t, types.DealClass_DEAL_CLASS_PRIVATE, client, "", 3, 1_000, 6, []types.PieceCommitment{
		commit(t, data), commit(t, data), commit(t, data),
	})
	f.end(t)
	f.begin(t)
	f.acceptAll(t, id, 3)
	gs, err := f.Keeper.ExportGenesis(f.Ctx)
	require.NoError(t, err)
	require.Len(t, gs.Slots, 3)
	return f, gs
}

// importer is a fresh chain whose x/nodes holds what the exporting chain's did: the same nodes and
// the bytes each has reserved.
func importer(t *testing.T, from *fixture) *fixture {
	t.Helper()
	g := newFixture(t)
	for id, info := range from.Nodes.byID {
		g.Nodes.byID[id] = info
		held, err := from.Nodes.ReservedCapacity(from.Ctx, id)
		require.NoError(t, err)
		if held > 0 {
			require.NoError(t, g.Nodes.reserved.Set(g.Ctx, id, held))
		}
	}
	return g
}

func TestInitGenesis_acceptsReservedBytesThatMatchTheReplicas(t *testing.T) {
	f, gs := exportedWithReplicas(t)
	g := importer(t, f)
	require.NoError(t, g.Keeper.InitGenesis(g.Ctx, *gs))
}

func TestInitGenesis_refusesReservedBytesThatDifferFromTheReplicas(t *testing.T) {
	f, gs := exportedWithReplicas(t)
	holder := gs.Slots[0].NodeId

	more := importer(t, f)
	held, err := more.Nodes.ReservedCapacity(more.Ctx, holder)
	require.NoError(t, err)
	require.NoError(t, more.Nodes.reserved.Set(more.Ctx, holder, held+1))
	require.ErrorContains(t, more.Keeper.InitGenesis(more.Ctx, *gs), "reserved")

	less := importer(t, f)
	require.NoError(t, less.Nodes.reserved.Set(less.Ctx, holder, held-1))
	require.ErrorContains(t, less.Keeper.InitGenesis(less.Ctx, *gs), "reserved")
}

func TestInitGenesis_refusesReservedBytesOnANodeWithNoReplicas(t *testing.T) {
	f, gs := exportedWithReplicas(t)
	gs.Slots, gs.Deals = nil, nil
	gs.Settlements, gs.QueueHead, gs.QueueTail = nil, 0, 0
	gs.Challenges, gs.Rechallenges = nil, nil
	g := importer(t, f)
	require.ErrorContains(t, g.Keeper.InitGenesis(g.Ctx, *gs), "reserved")
}
