package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/storage/types"
)

func TestPublicPinHasNoSubsidy(t *testing.T) {
	f := newFixture(t)
	f.init(t, func(gs *types.GenesisState) {
		gs.Params.SMinProviders = 1
		gs.Params.SFullProviders = 1
	})
	f.Emission.ceiling[1] = math.NewInt(1_000_000)
	f.threeNodes(t, 1<<20)
	client := acc(9)
	f.fund(client, 100_000)
	id := f.createDeal(t, types.DealClass_DEAL_CLASS_PUBLIC_PIN, client, "", 3, 1000, 1, []types.PieceCommitment{commit(t, payload(4))})
	f.end(t)
	f.begin(t)
	f.acceptAll(t, id, 3)
	for i := uint32(0); i < 3; i++ {
		f.proveSlot(t, id, i, payload(4))
	}
	f.end(t)
	f.Emission.epoch = 2
	f.begin(t)
	f.end(t)
	require.True(t, f.epochMint(t, 1).IsZero(), "user PUBLIC_PIN is not subsidised")
	f.requireInvariants(t)
}

func TestServiceSplitAndFinalSettlement(t *testing.T) {
	f := newFixture(t)
	f.init(t, func(gs *types.GenesisState) {
		gs.Params.SMinProviders = 1
		gs.Params.SFullProviders = 1
		gs.Params.DealFee = math.NewInt(1000)
	})
	f.Emission.ceiling[1] = math.NewInt(1_000_000)
	f.Emission.ceiling[2] = math.NewInt(1_000_000)
	nodes := f.threeNodes(t, 1<<20)
	client := acc(9)
	f.fund(client, 1_000_000)
	data := payload(5)
	// Duration 2: epoch 1 is proved, epoch 2 is missed, the missed epoch is refunded.
	id := f.createDeal(t, types.DealClass_DEAL_CLASS_PRIVATE, client, "", 3, 1000, 2, []types.PieceCommitment{
		commit(t, data), commit(t, data), commit(t, data),
	})
	// PRIVATE with three identical roots is allowed; each slot still has its own proof.
	require.Equal(t, 3, len(f.dealSlots(t, id)))
	f.end(t)
	f.begin(t)
	f.acceptAll(t, id, 3)
	for i := uint32(0); i < 3; i++ {
		f.proveSlot(t, id, i, data)
	}
	f.end(t)
	f.Emission.epoch = 2
	f.begin(t)
	f.end(t)
	// s = 1, cap = ceiling/1 = ceiling, so each slot mints its full 1000. Escrow also pays 1000.
	// Per slot gross to the split: 2000. 90/5/5 => 1800 / 100 / 100, times 3.
	require.Equal(t, math.NewInt(3000), f.epochMint(t, 1))
	var earned math.Int = math.ZeroInt()
	for _, n := range nodes {
		earned = earned.Add(f.Earnings.get(n.operator))
	}
	require.Equal(t, math.NewInt(5400), earned)
	require.Equal(t, math.NewInt(300), f.Bank.balanceOf(types.ArchiveModuleName))
	// Fee 1000 plus 5% of (escrow 3000 + mint 3000) = 300. Burn = 1300.
	require.Equal(t, math.NewInt(1300), f.Bank.burned)

	// Epoch 2 challenges were opened when epoch 1 settled. Nobody proves them.
	f.end(t)
	f.Emission.epoch = 3
	f.begin(t)
	f.end(t)
	// Missed epoch refunds 3 * 1000. Proved epoch was already paid.
	require.Equal(t, math.NewInt(3000), f.Earnings.get(client))
	require.Equal(t, types.DealStatus_DEAL_STATUS_EXPIRED, f.deal(t, id).Status)
	f.requireInvariants(t)
}

func (f *fixture) dealSlots(t *testing.T, id uint64) []types.Slot {
	t.Helper()
	var out []types.Slot
	for i := uint32(0); i < 3; i++ {
		out = append(out, f.slot(t, id, i))
	}
	return out
}

func TestExtendDoesNotMultiplyThisEpoch(t *testing.T) {
	f := newFixture(t)
	f.init(t, func(gs *types.GenesisState) {
		gs.Params.SMinProviders = 1
		gs.Params.SFullProviders = 1
	})
	f.Emission.ceiling[1] = math.NewInt(1_000_000)
	f.threeNodes(t, 1<<20)
	client := acc(9)
	f.fund(client, 10_000_000)
	data := payload(6)
	id := f.createDeal(t, types.DealClass_DEAL_CLASS_PRIVATE, client, "", 3, 1000, 1, []types.PieceCommitment{
		commit(t, data), commit(t, data), commit(t, data),
	})
	_, err := f.Msg.ExtendDeal(f.Ctx, &types.MsgExtendDeal{Signer: client.String(), DealId: id, ExtraEpochs: 5})
	require.NoError(t, err)
	require.Equal(t, uint64(6), f.deal(t, id).DurationEpochs)
	f.end(t)
	f.begin(t)
	f.acceptAll(t, id, 3)
	for i := uint32(0); i < 3; i++ {
		f.proveSlot(t, id, i, data)
	}
	f.end(t)
	f.Emission.epoch = 2
	f.begin(t)
	f.end(t)
	require.Equal(t, math.NewInt(3000), f.epochMint(t, 1), "an extension does not multiply the epoch being settled")
}

func TestQueueBackpressure(t *testing.T) {
	f := newFixture(t)
	f.init(t, func(gs *types.GenesisState) {
		gs.Params.MaxSettlementsPerBlock = 2
		gs.Params.KC = 8
		gs.Params.SMinProviders = 8
	})
	f.threeNodes(t, 1<<30)
	client := acc(9)
	f.fund(client, 100_000_000)
	const deals = 7
	ids := make([]uint64, deals)
	for i := 0; i < deals; i++ {
		ids[i] = f.createDeal(t, types.DealClass_DEAL_CLASS_PUBLIC_PIN, client, "", 3, 1000, 4, []types.PieceCommitment{commit(t, payload(byte(10+i)))})
	}
	f.end(t)
	f.begin(t)
	for _, id := range ids {
		f.acceptAll(t, id, 3)
	}
	f.end(t)
	f.Emission.epoch = 2
	f.begin(t) // enqueue every missed challenge
	q, err := f.Query.Queue(f.Ctx, &types.QueryQueueRequest{})
	require.NoError(t, err)
	require.GreaterOrEqual(t, q.Pending, uint64(20), "10x a per-block limit of 2")
	before := q.Pending
	f.end(t) // one block settles at most 2
	q, err = f.Query.Queue(f.Ctx, &types.QueryQueueRequest{})
	require.NoError(t, err)
	require.Equal(t, before-2, q.Pending)
	require.NotZero(t, q.Pending, "a 10x backlog does not settle in one block")
	blocks := 1
	for q.Pending > 0 {
		require.NoError(t, f.Keeper.EndBlock(f.Ctx))
		blocks++
		q, err = f.Query.Queue(f.Ctx, &types.QueryQueueRequest{})
		require.NoError(t, err)
		require.Less(t, blocks, 100)
	}
	require.Greater(t, blocks, 1)
	f.requireInvariants(t)
}

func TestSubsidyCapBinds(t *testing.T) {
	f := newFixture(t)
	f.init(t, func(gs *types.GenesisState) {
		gs.Params.SMinProviders = 2
		gs.Params.SFullProviders = 2
		gs.Params.DealFee = math.NewInt(1000)
	})
	// cap = ceiling / s_min = 100000 / 2 = 50000.
	f.Emission.ceiling[1] = math.NewInt(100_000)
	nodes := f.threeNodes(t, 1<<20)
	attacker := nodes[0]
	client := acc(9)
	f.fund(client, 100_000_000)
	big := payload(7)
	small := payload(8)
	bigID := f.createDeal(t, types.DealClass_DEAL_CLASS_PRIVATE, client, "", 3, 60_000, 1, []types.PieceCommitment{
		commit(t, big), commit(t, big), commit(t, big),
	})
	smallID := f.createDeal(t, types.DealClass_DEAL_CLASS_PRIVATE, client, "", 3, 1_000, 1, []types.PieceCommitment{
		commit(t, small), commit(t, small), commit(t, small),
	})
	f.end(t)
	f.begin(t)
	f.acceptAll(t, bigID, 3)
	f.acceptAll(t, smallID, 3)
	// Only the attacker proves the expensive deal. One other operator proves the cheap deal.
	proveOperator(t, f, bigID, attacker.operator, big)
	var honest *nodeInfo
	for _, n := range nodes {
		if !n.operator.Equals(attacker.operator) {
			honest = n
			break
		}
	}
	proveOperator(t, f, smallID, honest.operator, small)
	f.end(t)
	f.Emission.epoch = 2
	f.begin(t)
	f.end(t)
	// Uncapped, the attacker would mint 60000. The cap holds them to 50000.
	// The honest proof mints 1000. Total 51000, so the attacker is not the whole subsidy.
	require.Equal(t, math.NewInt(51_000), f.epochMint(t, 1))
	f.requireInvariants(t)
}

func proveOperator(t *testing.T, f *fixture, dealID uint64, operator sdk.AccAddress, data []byte) {
	t.Helper()
	for i := uint32(0); i < 3; i++ {
		slot := f.slot(t, dealID, i)
		if slot.Operator == operator.String() {
			f.proveSlot(t, dealID, i, data)
			return
		}
	}
	t.Fatalf("operator %s has no slot on deal %d", operator, dealID)
}

func TestEarlyNetworkSubsidyIsZero(t *testing.T) {
	f := newFixture(t)
	f.init(t, func(gs *types.GenesisState) {
		gs.Params.SMinProviders = 8
		gs.Params.SFullProviders = 16
	})
	f.Emission.ceiling[1] = math.NewInt(1_000_000)
	f.threeNodes(t, 1<<20)
	client := acc(9)
	f.fund(client, 100_000)
	data := payload(9)
	id := f.createDeal(t, types.DealClass_DEAL_CLASS_PRIVATE, client, "", 3, 1000, 1, []types.PieceCommitment{
		commit(t, data), commit(t, data), commit(t, data),
	})
	f.end(t)
	f.begin(t)
	f.acceptAll(t, id, 3)
	for i := uint32(0); i < 3; i++ {
		f.proveSlot(t, id, i, data)
	}
	f.end(t)
	f.Emission.epoch = 2
	f.begin(t)
	f.end(t)
	require.True(t, f.epochMint(t, 1).IsZero())
	f.requireInvariants(t)
}

// A settlement queue that lags past x/emission's ceiling window must still
// pay. The payments were minted into reserve when the epoch closed, so a
// pruned ceiling record cannot fail EndBlock and halt the chain.
func TestSettlement_paysFromTheReserveAfterTheCeilingIsPruned(t *testing.T) {
	f := newFixture(t)
	f.init(t, func(gs *types.GenesisState) {
		gs.Params.SMinProviders = 2
		gs.Params.SFullProviders = 2
		gs.Params.MaxSettlementsPerBlock = 1
	})
	f.Emission.ceiling[1] = math.NewInt(100_000)
	f.threeNodes(t, 1<<20)
	client := acc(9)
	f.fund(client, 100_000_000)
	data := payload(7)
	id := f.createDeal(t, types.DealClass_DEAL_CLASS_PRIVATE, client, "", 3, 1_000, 1, []types.PieceCommitment{
		commit(t, data), commit(t, data), commit(t, data),
	})
	f.end(t)
	f.begin(t)
	f.acceptAll(t, id, 3)
	for i := uint32(0); i < 3; i++ {
		f.proveSlot(t, id, i, data)
	}
	f.end(t)
	f.Emission.epoch = 2
	f.begin(t) // closes epoch 1 and reserves its storage payments
	reserved := f.Emission.minted[1]
	require.True(t, reserved.IsPositive(), "the epoch's payments were minted when it closed")
	f.requireInvariants(t)

	delete(f.Emission.ceiling, 1) // x/emission pruned the record
	for blocks := 0; ; blocks++ {
		require.NoError(t, f.Keeper.EndBlock(f.Ctx), "a pruned ceiling must not fail EndBlock")
		q, err := f.Query.Queue(f.Ctx, &types.QueryQueueRequest{})
		require.NoError(t, err)
		if q.Pending == 0 {
			break
		}
		require.Less(t, blocks, 50)
	}
	require.True(t, f.epochMint(t, 1).Equal(reserved), "every reserved payment was paid")
	f.requireInvariants(t)
}

// A node that proves and then releases its slot in the same epoch is not
// paid for that proof: at close the slot has no operator. Before this rule
// the payment was reserved at close and then never paid, which stranded it
// and broke the storage account invariant for good.
func TestCloseEpoch_doesNotPayAProofWhoseSlotWasReleased(t *testing.T) {
	f := newFixture(t)
	f.init(t, func(gs *types.GenesisState) {
		gs.Params.SMinProviders = 2
		gs.Params.SFullProviders = 2
	})
	f.Emission.ceiling[1] = math.NewInt(100_000)
	f.threeNodes(t, 1<<20)
	client := acc(9)
	f.fund(client, 100_000_000)
	data := payload(7)
	id := f.createDeal(t, types.DealClass_DEAL_CLASS_PRIVATE, client, "", 3, 1_000, 4, []types.PieceCommitment{
		commit(t, data), commit(t, data), commit(t, data),
	})
	f.end(t)
	f.begin(t)
	f.acceptAll(t, id, 3)
	for i := uint32(0); i < 3; i++ {
		f.proveSlot(t, id, i, data)
	}
	released := f.slot(t, id, 0)
	info := f.Nodes.byID[released.NodeId]
	_, err := f.Msg.ReleaseReplica(f.Ctx, &types.MsgReleaseReplica{
		Signer: info.hot.String(), NodeId: info.id, DealId: id, Slot: 0,
		Reason: types.ReleaseReason_RELEASE_REASON_LEGAL,
	})
	require.NoError(t, err)
	f.end(t)
	f.Emission.epoch = 2
	f.begin(t)
	for i := 0; i < 20; i++ {
		f.end(t)
	}
	q, err := f.Query.Queue(f.Ctx, &types.QueryQueueRequest{})
	require.NoError(t, err)
	require.Zero(t, q.Pending)
	require.True(t, f.epochMint(t, 1).Equal(f.Emission.minted[1]), "everything reserved was paid")
	f.requireInvariants(t)
}
