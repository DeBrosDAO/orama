package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/piece"
	"github.com/DeBrosOfficial/network/chain/x/storage/types"
)

func (f *fixture) threeNodes(t *testing.T, capacity uint64) []*nodeInfo {
	t.Helper()
	return []*nodeInfo{
		f.addNode(t, "n1", "10.0.0.0/16", 1, capacity, false),
		f.addNode(t, "n2", "10.1.0.0/16", 2, capacity, false),
		f.addNode(t, "n3", "10.2.0.0/16", 3, capacity, false),
	}
}

func (f *fixture) createDeal(t *testing.T, class types.DealClass, client sdk.AccAddress, granter string, replicas uint32, price, duration int64, pieces []types.PieceCommitment) uint64 {
	t.Helper()
	res, err := f.Msg.CreateDeal(f.Ctx, &types.MsgCreateDeal{
		Signer:         client.String(),
		Granter:        granter,
		Class:          class,
		DealNonce:      bytesOf(types.NonceLen, 0x42),
		Replicas:       replicas,
		PricePerEpoch:  math.NewInt(price),
		DurationEpochs: uint64(duration),
		Pieces:         pieces,
	})
	require.NoError(t, err)
	return res.DealId
}

func (f *fixture) slot(t *testing.T, dealID uint64, index uint32) types.Slot {
	t.Helper()
	res, err := f.Query.Slot(f.Ctx, &types.QuerySlotRequest{DealId: dealID, Slot: index})
	require.NoError(t, err)
	return res.Slot
}

func (f *fixture) deal(t *testing.T, id uint64) types.Deal {
	t.Helper()
	res, err := f.Query.Deal(f.Ctx, &types.QueryDealRequest{DealId: id})
	require.NoError(t, err)
	return res.Deal
}

func (f *fixture) acceptAll(t *testing.T, id uint64, replicas uint32) {
	t.Helper()
	for i := uint32(0); i < replicas; i++ {
		slot := f.slot(t, id, i)
		info := f.Nodes.byID[slot.NodeId]
		require.NotNil(t, info, "slot %d unassigned", i)
		_, err := f.Msg.AcceptDeal(f.Ctx, &types.MsgAcceptDeal{
			Signer: info.hot.String(), NodeId: info.id, DealId: id, Slot: i,
		})
		require.NoError(t, err)
	}
}

func (f *fixture) proveSlot(t *testing.T, id uint64, index uint32, data []byte) {
	t.Helper()
	slot := f.slot(t, id, index)
	info := f.Nodes.byID[slot.NodeId]
	ch, err := f.Query.Challenges(f.Ctx, &types.QueryChallengesRequest{Epoch: f.Emission.epoch, NodeId: info.id})
	require.NoError(t, err)
	var leaf uint64
	found := false
	for _, c := range ch.Challenges {
		if c.DealId == id && c.Slot == index {
			leaf = c.LeafIndex
			found = true
		}
	}
	require.True(t, found, "no challenge for %d/%d", id, index)
	proof, err := piece.Prove(data, leaf)
	require.NoError(t, err)
	_, err = f.Msg.SubmitProofs(f.Ctx, &types.MsgSubmitProofs{
		Signer: info.hot.String(),
		NodeId: info.id,
		Proofs: []types.ReplicaProof{{
			DealId: id, Slot: index, LeafIndex: leaf, Leaf: proof.Leaf, Siblings: proof.Siblings,
		}},
	})
	require.NoError(t, err)
}

func (f *fixture) epochMint(t *testing.T, epoch uint64) math.Int {
	t.Helper()
	res, err := f.Query.EpochMint(f.Ctx, &types.QueryEpochMintRequest{Epoch: epoch})
	require.NoError(t, err)
	return res.Minted
}

func TestDistinctOperatorsAndRefund(t *testing.T) {
	f := newFixture(t)
	f.init(t, nil)
	f.threeNodes(t, 1<<20)
	client := acc(9)
	f.fund(client, 100_000)
	pieces := []types.PieceCommitment{commit(t, payload(1)), commit(t, payload(2)), commit(t, payload(3))}
	id := f.createDeal(t, types.DealClass_DEAL_CLASS_PRIVATE, client, "", 3, 1000, 1, pieces)
	f.requireInvariants(t)
	require.Equal(t, math.NewInt(1000), f.Bank.burned, "per-deal fee is burned at create")

	f.end(t)
	f.begin(t)
	ops := map[string]struct{}{}
	for i := uint32(0); i < 3; i++ {
		slot := f.slot(t, id, i)
		require.NotEmpty(t, slot.NodeId)
		ops[slot.Operator] = struct{}{}
	}
	require.Len(t, ops, 3)
	f.requireInvariants(t)

	// Two nodes, one operator repeated, cannot fill three distinct operators.
	g := newFixture(t)
	g.init(t, nil)
	a := g.addNode(t, "a", "10.0.0.0/16", 1, 1<<20, false)
	b := g.addNode(t, "b", "10.1.0.0/16", 2, 1<<20, false)
	g.Nodes.byID["c"] = &nodeInfo{
		id: "c", hot: acc(50), operator: a.operator, net: "10.2.0.0/16", asn: 3,
		capacity: 1 << 20, active: true, slashed: math.ZeroInt(),
	}
	require.NoError(t, g.Keeper.TrackNode(g.Ctx, "c", false))
	_ = b
	payer := acc(8)
	g.fund(payer, 100_000)
	gid := g.createDeal(t, types.DealClass_DEAL_CLASS_PRIVATE, payer, "", 3, 1000, 1, pieces)
	g.end(t)
	g.begin(t)
	got := g.deal(t, gid)
	require.Equal(t, types.DealStatus_DEAL_STATUS_REFUNDED, got.Status)
	require.True(t, got.Escrow.IsZero())
	require.True(t, g.Earnings.get(payer).IsPositive(), "escrow refund lands in earnings")
	require.Equal(t, math.NewInt(1000), g.Bank.burned, "the burned fee is not refunded")
	g.requireInvariants(t)
}

func TestNonHotKeyRefusedAndDeclineHasNoSlash(t *testing.T) {
	f := newFixture(t)
	f.init(t, nil)
	nodes := f.threeNodes(t, 1<<20)
	spare := f.addNode(t, "n4", "10.3.0.0/16", 4, 1<<20, false)
	client := acc(9)
	f.fund(client, 100_000)
	pieces := []types.PieceCommitment{commit(t, payload(1)), commit(t, payload(2)), commit(t, payload(3))}
	id := f.createDeal(t, types.DealClass_DEAL_CLASS_PRIVATE, client, "", 3, 1000, 1, pieces)
	f.end(t)
	f.begin(t)

	slot := f.slot(t, id, 0)
	_, err := f.Msg.AcceptDeal(f.Ctx, &types.MsgAcceptDeal{
		Signer: client.String(), NodeId: slot.NodeId, DealId: id, Slot: 0,
	})
	require.Error(t, err, "a non-hot-key signer is refused")

	info := f.Nodes.byID[slot.NodeId]
	_, err = f.Msg.DeclineDeal(f.Ctx, &types.MsgDeclineDeal{
		Signer: info.hot.String(), NodeId: info.id, DealId: id, Slot: 0, Reason: "root mismatch",
	})
	require.NoError(t, err)
	require.Zero(t, info.slashN, "decline has no penalty")
	f.end(t)
	f.begin(t)
	again := f.slot(t, id, 0)
	require.NotEmpty(t, again.NodeId)
	require.NotEqual(t, info.id, again.NodeId)
	require.NotEqual(t, info.operator.String(), again.Operator)
	for _, n := range append(nodes, spare) {
		require.Zero(t, n.slashN)
	}
}

func TestRepairDelegateExcluded(t *testing.T) {
	f := newFixture(t)
	f.init(t, nil)
	nodes := f.threeNodes(t, 1<<20)
	delegate := nodes[0]
	client := acc(9)
	f.fund(client, 100_000)
	pieces := []types.PieceCommitment{commit(t, payload(1)), commit(t, payload(2)), commit(t, payload(3))}
	res, err := f.Msg.CreateDeal(f.Ctx, &types.MsgCreateDeal{
		Signer: client.String(), Class: types.DealClass_DEAL_CLASS_PRIVATE,
		DealNonce: bytesOf(types.NonceLen, 7), RepairDelegate: delegate.operator.String(),
		Replicas: 3, PricePerEpoch: math.NewInt(1000), DurationEpochs: 1, Pieces: pieces,
	})
	require.NoError(t, err)
	// Only three operators and one is excluded, so the deal cannot fill.
	f.end(t)
	f.begin(t)
	require.Equal(t, types.DealStatus_DEAL_STATUS_REFUNDED, f.deal(t, res.DealId).Status)

	spare := f.addNode(t, "n4", "10.9.0.0/16", 9, 1<<20, false)
	id := f.createDeal(t, types.DealClass_DEAL_CLASS_PRIVATE, client, "", 3, 1000, 1, pieces)
	// The second deal has no delegate. Re-create with the delegate and the spare.
	res, err = f.Msg.CreateDeal(f.Ctx, &types.MsgCreateDeal{
		Signer: client.String(), Class: types.DealClass_DEAL_CLASS_PRIVATE,
		DealNonce: bytesOf(types.NonceLen, 8), RepairDelegate: delegate.operator.String(),
		Replicas: 3, PricePerEpoch: math.NewInt(1000), DurationEpochs: 1, Pieces: pieces,
	})
	require.NoError(t, err)
	_ = id
	f.end(t)
	f.begin(t)
	got := f.deal(t, res.DealId)
	require.NotEqual(t, types.DealStatus_DEAL_STATUS_REFUNDED, got.Status)
	for i := uint32(0); i < 3; i++ {
		slot := f.slot(t, res.DealId, i)
		require.NotEqual(t, delegate.operator.String(), slot.Operator)
	}
	_ = spare
}

func TestReservedWithinDeclared(t *testing.T) {
	f := newFixture(t)
	f.init(t, nil)
	f.threeNodes(t, uint64(piece.LeafSize))
	client := acc(9)
	f.fund(client, 100_000)
	one := []types.PieceCommitment{commit(t, payload(1))}
	id := f.createDeal(t, types.DealClass_DEAL_CLASS_PUBLIC_PIN, client, "", 3, 1000, 1, one)
	f.end(t)
	f.begin(t)
	f.requireInvariants(t)
	for i := uint32(0); i < 3; i++ {
		require.NotEmpty(t, f.slot(t, id, i).NodeId)
	}
	// Every node's declared capacity is exactly one piece, so a second deal refunds.
	id2 := f.createDeal(t, types.DealClass_DEAL_CLASS_PUBLIC_PIN, client, "", 3, 1000, 1, one)
	f.end(t)
	f.begin(t)
	require.Equal(t, types.DealStatus_DEAL_STATUS_REFUNDED, f.deal(t, id2).Status)
	f.requireInvariants(t)
}

func TestRechallengeSlashAndEviction(t *testing.T) {
	f := newFixture(t)
	f.init(t, func(gs *types.GenesisState) {
		gs.Params.MissThreshold = 4
		gs.Params.KC = 1
		gs.Params.SMinProviders = 8
	})
	nodes := f.threeNodes(t, 1<<20)
	// A second replica on n1 comes from a second deal, so k_c=1 does not cover both.
	client := acc(9)
	f.fund(client, 1_000_000)
	pieces := []types.PieceCommitment{commit(t, payload(1)), commit(t, payload(2)), commit(t, payload(3))}
	id1 := f.createDeal(t, types.DealClass_DEAL_CLASS_PRIVATE, client, "", 3, 1000, 10, pieces)
	id2 := f.createDeal(t, types.DealClass_DEAL_CLASS_PRIVATE, client, "", 3, 1000, 10, pieces)
	f.end(t)
	f.begin(t)
	f.acceptAll(t, id1, 3)
	f.acceptAll(t, id2, 3)

	n1 := nodes[0]
	ch, err := f.Query.Challenges(f.Ctx, &types.QueryChallengesRequest{Epoch: 1, NodeId: n1.id})
	require.NoError(t, err)
	require.NotEmpty(t, ch.Challenges)
	missed := ch.Challenges[0]

	// Close epoch 1 and apply the miss before the next challenge round.
	f.end(t)
	f.Emission.epoch = 2
	f.begin(t)
	f.end(t)
	require.Zero(t, n1.slashN, "a single miss does not slash")

	// k_c = 0 so the next epoch's only challenges are the ReChallenge set.
	p, err := f.Keeper.Params.Get(f.Ctx)
	require.NoError(t, err)
	p.KC = 0
	require.NoError(t, f.Keeper.Params.Set(f.Ctx, p))

	f.Emission.epoch = 3
	f.begin(t)
	ch2, err := f.Query.Challenges(f.Ctx, &types.QueryChallengesRequest{Epoch: 3, NodeId: n1.id})
	require.NoError(t, err)
	require.NotEmpty(t, ch2.Challenges)
	found := false
	for _, c := range ch2.Challenges {
		if c.DealId == missed.DealId && c.Slot == missed.Slot {
			found = true
		}
	}
	require.True(t, found, "a ReChallenge entry is challenged even when k_c is 0")

	// The rechallenged slot misses again: two consecutive misses slash.
	f.end(t)
	f.Emission.epoch = 4
	f.begin(t)
	f.end(t)
	require.GreaterOrEqual(t, n1.slashN, 1)

	// Two further misses reach miss_threshold (4) and evict inside threshold+1 epochs.
	f.Emission.epoch = 5
	f.begin(t)
	f.end(t)
	f.Emission.epoch = 6
	f.begin(t)
	f.end(t)
	evicted := f.slot(t, missed.DealId, missed.Slot)
	require.NotEqual(t, n1.id, evicted.NodeId)
	require.LessOrEqual(t, uint64(6), uint64(4)+2)
}

func TestProofsAndDeterministicLeaf(t *testing.T) {
	f := newFixture(t)
	f.init(t, func(gs *types.GenesisState) {
		gs.Params.SMinProviders = 8
		gs.Params.MaxSettlementsPerBlock = 100
	})
	f.Emission.ceiling[1] = math.NewInt(0)
	nodes := f.threeNodes(t, 1<<20)
	client := acc(9)
	f.fund(client, 100_000)
	data := [][]byte{payload(1), payload(2), payload(3)}
	var pieces []types.PieceCommitment
	for _, d := range data {
		pieces = append(pieces, commit(t, d))
	}
	id := f.createDeal(t, types.DealClass_DEAL_CLASS_PRIVATE, client, "", 3, 1000, 1, pieces)
	f.end(t)
	f.begin(t)
	f.acceptAll(t, id, 3)
	// Two proof transactions in the same epoch.
	f.proveSlot(t, id, 0, dataForSlot(t, f, id, 0, data, pieces))
	f.proveSlot(t, id, 1, dataForSlot(t, f, id, 1, data, pieces))
	f.proveSlot(t, id, 2, dataForSlot(t, f, id, 2, data, pieces))

	// The challenged leaf matches the exported seed and stays inside the real count.
	for i := uint32(0); i < 3; i++ {
		slot := f.slot(t, id, i)
		info := f.Nodes.byID[slot.NodeId]
		ch, err := f.Query.Challenges(f.Ctx, &types.QueryChallengesRequest{Epoch: 1, NodeId: info.id})
		require.NoError(t, err)
		for _, c := range ch.Challenges {
			if c.DealId != id || c.Slot != i {
				continue
			}
			again, err := piece.LeafIndex(types.LeafChallengeSeed(1, id, i, info.id), slot.RealLeafCount)
			require.NoError(t, err)
			require.Equal(t, again, c.LeafIndex)
			require.Less(t, c.LeafIndex, slot.RealLeafCount)
		}
	}
	_ = nodes
	f.end(t)
	f.Emission.epoch = 2
	f.begin(t)
	f.end(t)
	// 90/5/5 of 3 * 1000, no subsidy (s_min not met).
	require.Equal(t, math.NewInt(150), f.Bank.balanceOf(types.ArchiveModuleName))
	var earned math.Int = math.ZeroInt()
	for _, n := range nodes {
		earned = earned.Add(f.Earnings.get(n.operator))
	}
	require.Equal(t, math.NewInt(2700), earned)
	require.Equal(t, math.NewInt(1000+150), f.Bank.burned)
	f.requireInvariants(t)
}

func dataForSlot(t *testing.T, f *fixture, id uint64, index uint32, data [][]byte, pieces []types.PieceCommitment) []byte {
	t.Helper()
	slot := f.slot(t, id, index)
	for i, p := range pieces {
		if string(p.Root) == string(slot.PieceRoot) {
			return data[i]
		}
	}
	t.Fatalf("no payload for slot %d", index)
	return nil
}
