package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	"github.com/DeBrosOfficial/network/chain/x/storage/types"
)

// registerProbation adds a fee-free registration as x/nodes holds it: it has the STORAGE role and
// no bond, so it is not active, and it is queued for x/storage to reconcile.
func (f *fixture) registerProbation(id, net string, asn uint32) *nodeInfo {
	info := f.registerUntracked(id, net, asn, 1<<20)
	info.active = false
	info.probation = true
	return info
}

func (f *fixture) threeProbationNodes() []*nodeInfo {
	return []*nodeInfo{
		f.registerProbation("p1", "10.0.0.0/16", 1),
		f.registerProbation("p2", "10.1.0.0/16", 2),
		f.registerProbation("p3", "10.2.0.0/16", 3),
	}
}

func (f *fixture) protocolDeal(t *testing.T, seed byte) uint64 {
	t.Helper()
	id, err := f.Keeper.CreateProtocolDeal(f.Ctx, types.DealClass_DEAL_CLASS_ARCHIVE, payload(seed), math.NewInt(1000), 4)
	require.NoError(t, err)
	return id
}

func TestSyncNodes_probationRegistrationIsTrackedAsProbation(t *testing.T) {
	f := newFixture(t)
	f.init(t, nil)
	f.registerProbation("p1", "10.0.0.0/16", 1)
	require.False(t, f.tracked(t, "p1"))
	f.end(t)
	f.begin(t)
	state, err := f.Keeper.Nodes.Get(f.Ctx, "p1")
	require.NoError(t, err)
	require.True(t, state.Probation, "a node with no bond is a probation node, not a bonded one")
	require.False(t, state.Graduated)
}

func TestSyncNodes_aNodeThatIsNeitherBondedNorOnProbationIsNotTracked(t *testing.T) {
	f := newFixture(t)
	f.init(t, nil)
	info := f.registerUntracked("n1", "10.0.0.0/16", 1, 1<<20)
	info.active = false
	f.end(t)
	f.begin(t)
	require.False(t, f.tracked(t, "n1"), "a partly bonded or jailed node is not a probation registration")
}

func TestProbation_nodesTakeProtocolDealSlotsUnderTheirCap(t *testing.T) {
	f := newFixture(t)
	f.init(t, nil)
	f.threeProbationNodes()
	first := f.protocolDeal(t, 1)
	f.end(t)
	f.begin(t)
	for i := uint32(0); i < 3; i++ {
		require.NotEmpty(t, f.slot(t, first, i).NodeId, "slot %d goes to a probation node", i)
	}

	second := f.protocolDeal(t, 2)
	f.end(t)
	f.begin(t)
	require.Equal(t, types.DealStatus_DEAL_STATUS_REFUNDED, f.deal(t, second).Status,
		"probation_slots is 1, so each probation node is full after one protocol slot")
	f.requireInvariants(t)
}

func TestProbation_nodesNeverHoldUserDeals(t *testing.T) {
	f := newFixture(t)
	f.init(t, nil)
	f.threeProbationNodes()
	client := acc(9)
	f.fund(client, 100_000)
	pieces := []types.PieceCommitment{commit(t, payload(1)), commit(t, payload(2)), commit(t, payload(3))}
	id := f.createDeal(t, types.DealClass_DEAL_CLASS_PRIVATE, client, "", 3, 1000, 1, pieces)
	f.end(t)
	f.begin(t)
	require.Equal(t, types.DealStatus_DEAL_STATUS_REFUNDED, f.deal(t, id).Status,
		"a probation node has no bond to slash, so it takes no user deal")
}

func TestProbation_aNodeThatBondsGraduatesAndFreesItsCaps(t *testing.T) {
	f := newFixture(t)
	f.init(t, nil)
	nodes := f.threeProbationNodes()
	first := f.protocolDeal(t, 1)
	f.end(t)
	f.begin(t)
	held := f.slot(t, first, 0)
	require.NotEmpty(t, held.NodeId)
	count, err := f.Keeper.ProbationNode.Get(f.Ctx, held.NodeId)
	require.NoError(t, err)
	require.Equal(t, uint64(1), count)

	for _, n := range nodes {
		n.active, n.probation = true, false
		f.Nodes.touch(n.id)
	}
	f.end(t)
	f.begin(t)
	for _, n := range nodes {
		state, err := f.Keeper.Nodes.Get(f.Ctx, n.id)
		require.NoError(t, err)
		require.False(t, state.Probation)
		require.True(t, state.Graduated, "a bonded probation node is an ordinary provider")
		has, err := f.Keeper.ProbationNode.Has(f.Ctx, n.id)
		require.NoError(t, err)
		require.False(t, has, "its slot no longer counts against the probation caps")
	}
	for _, key := range []string{held.Operator} {
		has, err := f.Keeper.ProbationOp.Has(f.Ctx, key)
		require.NoError(t, err)
		require.False(t, has)
	}

	second := f.protocolDeal(t, 2)
	f.end(t)
	f.begin(t)
	require.NotEqual(t, types.DealStatus_DEAL_STATUS_REFUNDED, f.deal(t, second).Status, "graduated nodes take more than one slot")
	for i := uint32(0); i < 3; i++ {
		require.NotEmpty(t, f.slot(t, second, i).NodeId)
	}
	f.requireInvariants(t)
}

func TestProbation_aNodeThatProvesNothingIsJailedAndFreesItsCaps(t *testing.T) {
	f := newFixture(t)
	f.init(t, nil)
	f.threeProbationNodes()
	first := f.protocolDeal(t, 1)
	f.end(t)
	f.begin(t)
	holder := f.slot(t, first, 0)
	require.NotEmpty(t, holder.NodeId)

	f.Emission.epoch = 20
	f.end(t)
	f.begin(t)
	require.True(t, f.Nodes.byID[holder.NodeId].jailed, "a probation node that proved nothing is jailed at expiry")
	has, err := f.Keeper.ProbationOp.Has(f.Ctx, holder.Operator)
	require.NoError(t, err)
	require.False(t, has, "the jailed node's slot no longer charges its operator")
	has, err = f.Keeper.ProbationNet.Has(f.Ctx, holder.Network16)
	require.NoError(t, err)
	require.False(t, has)
}

func TestProbation_aGraduatedNodeWithoutABondKeepsItsStateAndGetsNoSlots(t *testing.T) {
	f := newFixture(t)
	f.init(t, func(gs *types.GenesisState) {
		gs.Params.ProbationExpiryEpochs = 2
		gs.Params.ProbationDeposit = math.NewInt(100)
		gs.Params.SMinProviders = 1
		gs.Params.SFullProviders = 1
	})
	f.Emission.ceiling[1] = math.NewInt(1_000_000)
	nodes := f.threeProbationNodes()
	data := payload(9)
	id, err := f.Keeper.CreateProtocolDeal(f.Ctx, types.DealClass_DEAL_CLASS_PUBLIC_PIN, data, math.NewInt(1000), 6)
	require.NoError(t, err)
	f.end(t)
	f.begin(t)
	f.acceptAll(t, id, 3)
	for i := uint32(0); i < 3; i++ {
		f.proveSlot(t, id, i, data)
	}
	f.Emission.epoch = 2
	f.end(t)
	f.begin(t)
	f.end(t)
	f.Emission.epoch = 3
	f.end(t)
	f.begin(t)
	state, err := f.Keeper.Nodes.Get(f.Ctx, nodes[0].id)
	require.NoError(t, err)
	require.True(t, state.Graduated, "a probation node that proved storage outlives probation")

	f.Nodes.touch(nodes[0].id)
	f.end(t)
	f.begin(t)
	require.True(t, f.tracked(t, nodes[0].id), "it is not untracked and re-registered as a fresh probation node")
	state, err = f.Keeper.Nodes.Get(f.Ctx, nodes[0].id)
	require.NoError(t, err)
	require.True(t, state.Graduated)
	require.False(t, state.Probation)
}
