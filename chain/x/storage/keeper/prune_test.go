package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"

	"github.com/DeBrosOfficial/network/chain/x/storage/types"
)

// requireDealFinished checks a finished deal is gone from the state: its deal and slot queries answer
// "does not exist", as they do for a deal that never existed.
func (f *fixture) requireDealFinished(t *testing.T, id uint64, msgAndArgs ...any) {
	t.Helper()
	_, err := f.Query.Deal(f.Ctx, &types.QueryDealRequest{DealId: id})
	require.Equal(t, codes.NotFound, status.Code(err), msgAndArgs...)
	for index := uint32(0); index < 3; index++ {
		_, err = f.Query.Slot(f.Ctx, &types.QuerySlotRequest{DealId: id, Slot: index})
		require.Equal(t, codes.NotFound, status.Code(err), "slot %d of finished deal %d", index, id)
	}
}

// runExpiredDeal runs a 3-replica deal for one epoch, proves it and lets it end.
func runExpiredDeal(t *testing.T) (*fixture, uint64) {
	t.Helper()
	f := newFixture(t)
	f.init(t, func(gs *types.GenesisState) {
		gs.Params.SMinProviders = 1
		gs.Params.SFullProviders = 1
	})
	f.Emission.ceiling[1] = math.NewInt(1_000_000)
	f.threeNodes(t, 1<<20)
	client := acc(9)
	f.fund(client, 1_000_000)
	data := payload(5)
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
	return f, id
}

func TestExpiredDeal_isDeletedWithItsSlotsOnceSettled(t *testing.T) {
	f, id := runExpiredDeal(t)
	require.Equal(t, types.DealStatus_DEAL_STATUS_ACTIVE, f.deal(t, id).Status)

	f.Emission.epoch = 2
	f.begin(t)
	f.end(t) // epoch 1 settles and the deal ends
	f.requireDealFinished(t, id)

	deals, err := f.Query.Deals(f.Ctx, &types.QueryDealsRequest{})
	require.NoError(t, err)
	require.Empty(t, deals.Deals, "a finished deal is not listed")
	exported, err := f.Keeper.ExportGenesis(f.Ctx)
	require.NoError(t, err)
	require.Empty(t, exported.Deals)
	require.Empty(t, exported.Slots)
	f.requireInvariants(t)
	for _, n := range []string{"n1", "n2", "n3"} {
		held, err := f.Keeper.ReplicaCount.Has(f.Ctx, n)
		require.NoError(t, err)
		require.False(t, held, "node %s holds no replica of a deleted deal", n)
	}
}

func TestDealExpiry_waitsForTheSettlementQueueToDrain(t *testing.T) {
	f, id := runExpiredDeal(t)
	f.Emission.epoch = 2
	f.begin(t)
	pending, err := f.Keeper.QueuePending.Has(f.Ctx, id)
	require.NoError(t, err)
	require.True(t, pending, "closing the epoch queued the deal's payments")
	require.NotEqual(t, types.DealStatus_DEAL_STATUS_EXPIRED, f.deal(t, id).Status)
	f.end(t)
	f.requireDealFinished(t, id)
	pending, err = f.Keeper.QueuePending.Has(f.Ctx, id)
	require.NoError(t, err)
	require.False(t, pending)
}

func TestExtendDeal_movesTheExpiryToTheNewEndEpoch(t *testing.T) {
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
	f.end(t)
	f.begin(t)
	f.acceptAll(t, id, 3)
	f.end(t)

	for epoch := uint64(2); epoch <= 4; epoch++ {
		f.Emission.epoch = epoch
		f.begin(t)
		f.end(t)
		require.Equal(t, types.DealStatus_DEAL_STATUS_ACTIVE, f.deal(t, id).Status, "still running in epoch %d", epoch)
	}
	end := f.deal(t, id).EndEpoch
	has, err := f.Keeper.DealExpiry.Has(f.Ctx, collections.Join(end, id))
	require.NoError(t, err)
	require.True(t, has, "the deal is indexed under its extended end epoch")
	has, err = f.Keeper.DealExpiry.Has(f.Ctx, collections.Join(end-5, id))
	require.NoError(t, err)
	require.False(t, has, "and no longer under the old one")
}

func TestAcceptWindow_releasesOnlyTheSlotsNobodyAccepted(t *testing.T) {
	f := newFixture(t)
	f.init(t, func(gs *types.GenesisState) { gs.Params.AcceptWindowBlocks = 6 })
	f.Emission.ceiling[1] = math.NewInt(1_000_000)
	f.threeNodes(t, 1<<20)
	client := acc(9)
	f.fund(client, 10_000_000)
	data := payload(7)
	id := f.createDeal(t, types.DealClass_DEAL_CLASS_PRIVATE, client, "", 3, 1000, 5, []types.PieceCommitment{
		commit(t, data), commit(t, data), commit(t, data),
	})
	f.end(t)
	f.begin(t)
	for i := uint32(0); i < 3; i++ {
		require.Equal(t, types.SlotStatus_SLOT_STATUS_ASSIGNED, f.slot(t, id, i).Status)
	}
	f.acceptAll(t, id, 1)
	firstAssign := f.slot(t, id, 1).AssignHeight
	for i := 0; i < 10; i++ {
		f.end(t)
		f.begin(t)
	}
	kept := f.slot(t, id, 0)
	require.True(t, kept.Accepted)
	require.Equal(t, types.SlotStatus_SLOT_STATUS_ACTIVE, kept.Status, "an accepted slot is never expired")
	for i := uint32(1); i < 3; i++ {
		lapsed := f.slot(t, id, i)
		require.Greater(t, lapsed.AssignHeight, firstAssign, "slot %d was released and assigned again", i)
	}
	f.requireInvariants(t)
}
