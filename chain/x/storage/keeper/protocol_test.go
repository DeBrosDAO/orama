package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	"github.com/DeBrosOfficial/network/chain/piece"
	"github.com/DeBrosOfficial/network/chain/x/storage/types"
)

func TestUsersCannotCreateProtocolDeals(t *testing.T) {
	f := newFixture(t)
	f.init(t, nil)
	client := acc(9)
	f.fund(client, 100_000)
	_, err := f.Msg.CreateDeal(f.Ctx, &types.MsgCreateDeal{
		Signer: client.String(), Class: types.DealClass_DEAL_CLASS_ARCHIVE,
		DealNonce: bytesOf(types.NonceLen, 1), Replicas: 3,
		PricePerEpoch: math.NewInt(1000), DurationEpochs: 1,
		Pieces: []types.PieceCommitment{commit(t, payload(1))},
	})
	require.Error(t, err)

	f.threeNodes(t, 1<<20)
	id, err := f.Keeper.CreateProtocolDeal(f.Ctx, types.DealClass_DEAL_CLASS_ARCHIVE, payload(2), math.NewInt(1000), 1)
	require.NoError(t, err)
	got := f.deal(t, id)
	require.True(t, got.Protocol)
	require.Equal(t, types.DealClass_DEAL_CLASS_ARCHIVE, got.Class)
	require.True(t, got.Escrow.IsZero())
}

func TestProtocolSlotsDistinctNetworkAndASN(t *testing.T) {
	f := newFixture(t)
	f.init(t, nil)
	f.addNode(t, "a", "10.0.0.0/16", 1, 1<<20, false)
	f.addNode(t, "b", "10.0.0.0/16", 2, 1<<20, false)
	f.addNode(t, "c", "10.1.0.0/16", 3, 1<<20, false)
	_, err := f.Keeper.CreateProtocolDeal(f.Ctx, types.DealClass_DEAL_CLASS_PUBLIC_PIN, payload(3), math.NewInt(1000), 1)
	require.NoError(t, err)
	f.end(t)
	f.begin(t)
	// Only two /16s, so the protocol deal cannot fill three diverse slots.
	require.Equal(t, types.DealStatus_DEAL_STATUS_REFUNDED, f.deal(t, 1).Status)

	g := newFixture(t)
	g.init(t, nil)
	g.addNode(t, "a", "10.0.0.0/16", 1, 1<<20, false)
	g.addNode(t, "b", "10.1.0.0/16", 2, 1<<20, false)
	g.addNode(t, "c", "10.2.0.0/16", 3, 1<<20, false)
	g.addNode(t, "dup", "10.0.0.0/16", 1, 1<<20, false)
	id, err := g.Keeper.CreateProtocolDeal(g.Ctx, types.DealClass_DEAL_CLASS_ARCHIVE, payload(4), math.NewInt(1000), 1)
	require.NoError(t, err)
	g.end(t)
	g.begin(t)
	require.NotEqual(t, types.DealStatus_DEAL_STATUS_REFUNDED, g.deal(t, id).Status)
	nets := map[string]struct{}{}
	asns := map[uint32]struct{}{}
	ops := map[string]struct{}{}
	for i := uint32(0); i < 3; i++ {
		slot := g.slot(t, id, i)
		require.NotEmpty(t, slot.NodeId)
		nets[slot.Network16] = struct{}{}
		asns[slot.Asn] = struct{}{}
		ops[slot.Operator] = struct{}{}
	}
	require.Len(t, nets, 3)
	require.Len(t, asns, 3)
	require.Len(t, ops, 3)
	g.requireInvariants(t)
}

func TestProtocolDealsCreatedOnSchedule(t *testing.T) {
	f := newFixture(t)
	f.init(t, func(gs *types.GenesisState) {
		gs.Params.ProtocolEveryEpochs = 1
		gs.Params.ProtocolPieceBytes = uint64(piece.LeafSize)
		gs.Params.ProtocolPricePerEpoch = math.NewInt(1000)
		gs.Params.ProtocolDurationEpochs = 1
	})
	f.threeNodes(t, 1<<20)
	f.Emission.epoch = 2
	f.begin(t)
	archive := f.deal(t, 1)
	pin := f.deal(t, 2)
	require.True(t, archive.Protocol)
	require.True(t, pin.Protocol)
	classes := map[types.DealClass]bool{archive.Class: true, pin.Class: true}
	require.True(t, classes[types.DealClass_DEAL_CLASS_ARCHIVE])
	require.True(t, classes[types.DealClass_DEAL_CLASS_PUBLIC_PIN])
}

func TestProbationCapsExpiryAndDeposit(t *testing.T) {
	f := newFixture(t)
	f.init(t, func(gs *types.GenesisState) {
		gs.Params.ProbationSlots = 1
		gs.Params.ProbationOperatorCap = 1
		gs.Params.ProbationExpiryEpochs = 2
		gs.Params.ProbationDeposit = math.NewInt(100)
		gs.Params.SMinProviders = 1
		gs.Params.SFullProviders = 1
	})
	f.Emission.ceiling[1] = math.NewInt(1_000_000)
	f.addNode(t, "r1", "10.0.0.0/16", 1, 1<<20, false)
	f.addNode(t, "r2", "10.1.0.0/16", 2, 1<<20, false)
	prob := f.addNode(t, "p1", "10.2.0.0/16", 3, 1<<20, true)
	// Same operator as p1, also on probation: the operator cap blocks a second protocol slot.
	p2 := f.addNode(t, "p2", "10.3.0.0/16", 4, 1<<20, true)
	f.Nodes.byID["p2"].operator = prob.operator

	first, err := f.Keeper.CreateProtocolDeal(f.Ctx, types.DealClass_DEAL_CLASS_ARCHIVE, payload(1), math.NewInt(1000), 4)
	require.NoError(t, err)
	f.end(t)
	f.begin(t)
	require.NotEqual(t, types.DealStatus_DEAL_STATUS_REFUNDED, f.deal(t, first).Status)
	var sawProb bool
	for i := uint32(0); i < 3; i++ {
		if f.slot(t, first, i).NodeId == prob.id {
			sawProb = true
		}
	}
	require.True(t, sawProb, "the only way to fill three operators includes the probation node")

	second, err := f.Keeper.CreateProtocolDeal(f.Ctx, types.DealClass_DEAL_CLASS_ARCHIVE, payload(2), math.NewInt(1000), 4)
	require.NoError(t, err)
	f.end(t)
	f.begin(t)
	require.Equal(t, types.DealStatus_DEAL_STATUS_REFUNDED, f.deal(t, second).Status, "probation caps leave fewer than 3 eligible nodes")
	_ = p2

	// A probation node that never proves is jailed at expiry. Registered in epoch 1;
	// expiry is 2 epochs, so epoch 3 jails it. Use a fresh node that is not assigned.
	idle := f.addNode(t, "idle", "10.4.0.0/16", 5, 1<<20, true)
	f.Emission.epoch = 3
	f.end(t)
	f.begin(t)
	require.True(t, idle.jailed)
	require.Empty(t, f.Deposits.released)

	// A node that proves locks the record deposit from earnings, then recovers it at expiry.
	g := newFixture(t)
	g.init(t, func(gs *types.GenesisState) {
		gs.Params.ProbationSlots = 4
		gs.Params.ProbationOperatorCap = 4
		gs.Params.ProbationNetwork16Cap = 4
		gs.Params.ProbationAsnCap = 4
		gs.Params.ProbationExpiryEpochs = 2
		gs.Params.ProbationDeposit = math.NewInt(100)
		gs.Params.SMinProviders = 1
		gs.Params.SFullProviders = 1
	})
	g.Emission.ceiling[1] = math.NewInt(1_000_000)
	a := g.addNode(t, "a", "10.0.0.0/16", 1, 1<<20, true)
	g.addNode(t, "b", "10.1.0.0/16", 2, 1<<20, true)
	g.addNode(t, "c", "10.2.0.0/16", 3, 1<<20, true)
	data := payload(9)
	id, err := g.Keeper.CreateProtocolDeal(g.Ctx, types.DealClass_DEAL_CLASS_PUBLIC_PIN, data, math.NewInt(1000), 4)
	require.NoError(t, err)
	g.end(t)
	g.begin(t)
	g.acceptAll(t, id, 3)
	for i := uint32(0); i < 3; i++ {
		g.proveSlot(t, id, i, data)
	}
	g.Emission.epoch = 2
	g.end(t)
	g.begin(t)
	g.end(t)
	require.Contains(t, g.Deposits.locked, "storage/probation/"+a.id)
	g.Emission.epoch = 3
	g.end(t)
	g.begin(t)
	require.Contains(t, g.Deposits.released, "storage/probation/"+a.id)
	require.False(t, a.jailed)
}

func TestDealAuthorizationLimits(t *testing.T) {
	f := newFixture(t)
	f.init(t, func(gs *types.GenesisState) {
		gs.Params.DealFee = math.NewInt(1000)
		gs.Params.MinDealBytes = uint64(piece.LeafSize)
	})
	f.threeNodes(t, 1<<20)
	granter := acc(9)
	grantee := acc(10)
	f.fund(granter, 100_000)
	// price 1000 * 3 replicas * 1 epoch + fee 1000 = 4000.
	_, err := f.Msg.GrantDealAuthorization(f.Ctx, &types.MsgGrantDealAuthorization{
		Signer: granter.String(), Grantee: grantee.String(),
		SpendLimit: math.NewInt(4000), MaxPieceBytes: uint64(piece.LeafSize),
		MaxDurationEpochs: 1, Replicas: 3,
	})
	require.NoError(t, err)
	pieceCommit := commit(t, payload(1))
	id := f.createDeal(t, types.DealClass_DEAL_CLASS_PRIVATE, grantee, granter.String(), 3, 1000, 1, []types.PieceCommitment{
		pieceCommit, pieceCommit, pieceCommit,
	})
	require.NotZero(t, id)
	_, err = f.Msg.CreateDeal(f.Ctx, &types.MsgCreateDeal{
		Signer: grantee.String(), Granter: granter.String(),
		Class: types.DealClass_DEAL_CLASS_PRIVATE, DealNonce: bytesOf(types.NonceLen, 3),
		Replicas: 3, PricePerEpoch: math.NewInt(1000), DurationEpochs: 1,
		Pieces: []types.PieceCommitment{pieceCommit, pieceCommit, pieceCommit},
	})
	require.Error(t, err, "spend limit is exhausted")

	_, err = f.Msg.RevokeDealAuthorization(f.Ctx, &types.MsgRevokeDealAuthorization{
		Signer: granter.String(), Grantee: grantee.String(),
	})
	require.NoError(t, err)
	f.fund(granter, 100_000)
	_, err = f.Msg.CreateDeal(f.Ctx, &types.MsgCreateDeal{
		Signer: grantee.String(), Granter: granter.String(),
		Class: types.DealClass_DEAL_CLASS_PRIVATE, DealNonce: bytesOf(types.NonceLen, 4),
		Replicas: 3, PricePerEpoch: math.NewInt(1000), DurationEpochs: 1,
		Pieces: []types.PieceCommitment{pieceCommit, pieceCommit, pieceCommit},
	})
	require.Error(t, err, "revoked grant cannot spend")
}

func TestReleaseIsRateLimitedAndDoesNotSlash(t *testing.T) {
	f := newFixture(t)
	f.init(t, func(gs *types.GenesisState) {
		gs.Params.MaxReleasesPerEpoch = 1
	})
	nodes := f.threeNodes(t, 1<<20)
	spare := f.addNode(t, "n4", "10.3.0.0/16", 4, 1<<20, false)
	client := acc(9)
	f.fund(client, 100_000)
	pieces := []types.PieceCommitment{commit(t, payload(1)), commit(t, payload(2)), commit(t, payload(3))}
	id := f.createDeal(t, types.DealClass_DEAL_CLASS_PRIVATE, client, "", 3, 1000, 4, pieces)
	f.end(t)
	f.begin(t)
	slot := f.slot(t, id, 0)
	info := f.Nodes.byID[slot.NodeId]
	_, err := f.Msg.ReleaseReplica(f.Ctx, &types.MsgReleaseReplica{
		Signer: info.hot.String(), NodeId: info.id, DealId: id, Slot: 0,
		Reason: types.ReleaseReason_RELEASE_REASON_LEGAL,
	})
	require.NoError(t, err)
	require.Zero(t, info.slashN)
	_, err = f.Msg.ReleaseReplica(f.Ctx, &types.MsgReleaseReplica{
		Signer: info.hot.String(), NodeId: info.id, DealId: id, Slot: 1,
		Reason: types.ReleaseReason_RELEASE_REASON_LEGAL,
	})
	require.Error(t, err, "second release in the epoch is refused")
	f.end(t)
	f.begin(t)
	again := f.slot(t, id, 0)
	require.NotEmpty(t, again.NodeId)
	require.NotEqual(t, info.id, again.NodeId)
	require.NotEqual(t, info.operator.String(), again.Operator)
	_ = nodes
	_ = spare
}
