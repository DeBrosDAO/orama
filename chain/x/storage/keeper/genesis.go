package keeper

import (
	"fmt"
	"sort"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/storage/types"
)

// InitGenesis loads genesis and rebuilds the replica index, reserved bytes and
// probation counters from slots so those indexes cannot drift from the slots.
func (k Keeper) InitGenesis(ctx sdk.Context, gen types.GenesisState) error {
	if err := gen.Validate(); err != nil {
		return fmt.Errorf("invalid storage genesis: %w", err)
	}
	if err := k.Params.Set(ctx, gen.Params); err != nil {
		return err
	}
	if err := k.NextDealID.Set(ctx, gen.NextDealId); err != nil {
		return err
	}
	last := gen.LastEpoch
	if last == 0 {
		epoch, err := k.currentEpoch(ctx)
		if err != nil {
			return err
		}
		last = epoch
	}
	if err := k.LastEpoch.Set(ctx, last); err != nil {
		return err
	}
	if err := k.LastProtocolEpoch.Set(ctx, gen.LastProtocolEpoch); err != nil {
		return err
	}
	if err := k.QueueHead.Set(ctx, gen.QueueHead); err != nil {
		return err
	}
	if err := k.QueueTail.Set(ctx, gen.QueueTail); err != nil {
		return err
	}
	if err := k.ArchiveFund.Set(ctx, types.NormalizeInt(gen.ArchiveFund)); err != nil {
		return err
	}
	if err := k.DealCountHeight.Set(ctx, gen.DealCountHeight); err != nil {
		return err
	}
	if err := k.DealsInBlock.Set(ctx, gen.DealsCreatedInBlock); err != nil {
		return err
	}
	for _, d := range gen.Deals {
		if err := k.Deals.Set(ctx, d.Id, d); err != nil {
			return err
		}
	}
	for _, s := range gen.Slots {
		s.ReplicaIndexed = false
		s.ReplicaSeq = 0
		if err := k.Slots.Set(ctx, collections.Join(s.DealId, s.Index), s); err != nil {
			return err
		}
	}
	for _, a := range gen.Authorizations {
		if err := k.Auths.Set(ctx, collections.Join(a.Granter, a.Grantee), a); err != nil {
			return err
		}
	}
	for _, n := range gen.Nodes {
		if err := k.Nodes.Set(ctx, n.NodeId, n); err != nil {
			return err
		}
	}
	for _, c := range gen.Challenges {
		key := collections.Join(c.Epoch, challengeID(c.NodeId, c.DealId, c.Slot))
		if err := k.Challenges.Set(ctx, key, c.Record); err != nil {
			return err
		}
	}
	for _, r := range gen.Rechallenges {
		if err := k.Rechallenge.Set(ctx, collections.Join(r.NodeId, rechallengeID(r.DealId, r.Slot))); err != nil {
			return err
		}
	}
	for _, s := range gen.Settlements {
		if err := k.Queue.Set(ctx, s.Seq, s); err != nil {
			return err
		}
		if err := k.addQueuePending(ctx, s.DealId, 1); err != nil {
			return err
		}
	}
	for _, m := range gen.EpochMints {
		if err := k.EpochMinted.Set(ctx, m.Epoch, m.Minted); err != nil {
			return err
		}
	}
	for _, m := range gen.OperatorMints {
		if err := k.OperatorMinted.Set(ctx, collections.Join(m.Epoch, m.Operator), m.Minted); err != nil {
			return err
		}
	}
	for _, r := range gen.ReleaseCounts {
		if err := k.Releases.Set(ctx, collections.Join(r.Epoch, r.NodeId), r.Count); err != nil {
			return err
		}
	}
	if err := k.rebuildFromSlots(ctx); err != nil {
		return err
	}
	return nil
}

func (k Keeper) rebuildFromSlots(ctx sdk.Context) error {
	var slots []types.Slot
	if err := k.Slots.Walk(ctx, nil, func(_ collections.Pair[uint64, uint32], slot types.Slot) (bool, error) {
		slots = append(slots, slot)
		return false, nil
	}); err != nil {
		return err
	}
	sort.Slice(slots, func(i, j int) bool {
		if slots[i].NodeId != slots[j].NodeId {
			return slots[i].NodeId < slots[j].NodeId
		}
		if slots[i].DealId != slots[j].DealId {
			return slots[i].DealId < slots[j].DealId
		}
		return slots[i].Index < slots[j].Index
	})
	for i := range slots {
		slot := slots[i]
		if slot.NodeId == "" {
			continue
		}
		if slot.Status != types.SlotStatus_SLOT_STATUS_ASSIGNED && slot.Status != types.SlotStatus_SLOT_STATUS_ACTIVE {
			continue
		}
		if err := k.adjustReserved(ctx, slot.NodeId, int64(slot.PieceBytes)); err != nil {
			return err
		}
		if err := k.addReplica(ctx, &slot); err != nil {
			return err
		}
		if err := k.saveSlot(ctx, slot); err != nil {
			return err
		}
		deal, err := k.loadDeal(ctx, slot.DealId)
		if err != nil {
			return err
		}
		if deal.Protocol {
			state, err := k.Nodes.Get(ctx, slot.NodeId)
			if err != nil {
				return err
			}
			if err := k.noteProbationAssign(ctx, state, slot.Operator, slot.Network16, slot.Asn, 1); err != nil {
				return err
			}
		}
		if slot.Status == types.SlotStatus_SLOT_STATUS_ASSIGNED || (slot.Status == types.SlotStatus_SLOT_STATUS_UNASSIGNED && deal.Status == types.DealStatus_DEAL_STATUS_OPEN) {
			if err := k.Pending.Set(ctx, deal.Id); err != nil {
				return err
			}
		}
	}
	// Deals that still have an unassigned slot and are open stay pending.
	for i := range slots {
		if slots[i].NodeId == "" {
			deal, err := k.loadDeal(ctx, slots[i].DealId)
			if err != nil {
				return err
			}
			if deal.Status == types.DealStatus_DEAL_STATUS_OPEN || deal.Status == types.DealStatus_DEAL_STATUS_ACTIVE {
				if err := k.Pending.Set(ctx, deal.Id); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// ExportGenesis writes the current state. Derived indexes are included only as
// the reserved totals and the slots' replica flags, which InitGenesis rebuilds.
func (k Keeper) ExportGenesis(ctx sdk.Context) (*types.GenesisState, error) {
	p, err := k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	gs := types.DefaultGenesisState()
	gs.Params = p
	gs.NextDealId, err = k.NextDealID.Get(ctx)
	if err != nil {
		return nil, err
	}
	gs.LastEpoch, err = k.LastEpoch.Get(ctx)
	if err != nil {
		return nil, err
	}
	gs.LastProtocolEpoch, err = k.LastProtocolEpoch.Get(ctx)
	if err != nil {
		return nil, err
	}
	gs.QueueHead, err = k.QueueHead.Get(ctx)
	if err != nil {
		return nil, err
	}
	gs.QueueTail, err = k.QueueTail.Get(ctx)
	if err != nil {
		return nil, err
	}
	gs.ArchiveFund, err = k.ArchiveFund.Get(ctx)
	if err != nil {
		return nil, err
	}
	gs.DealCountHeight, err = k.DealCountHeight.Get(ctx)
	if err != nil {
		return nil, err
	}
	gs.DealsCreatedInBlock, err = k.DealsInBlock.Get(ctx)
	if err != nil {
		return nil, err
	}
	if err := k.Deals.Walk(ctx, nil, func(_ uint64, d types.Deal) (bool, error) {
		gs.Deals = append(gs.Deals, d)
		return false, nil
	}); err != nil {
		return nil, err
	}
	if err := k.Slots.Walk(ctx, nil, func(_ collections.Pair[uint64, uint32], s types.Slot) (bool, error) {
		gs.Slots = append(gs.Slots, s)
		return false, nil
	}); err != nil {
		return nil, err
	}
	if err := k.Auths.Walk(ctx, nil, func(_ collections.Pair[string, string], a types.DealAuthorization) (bool, error) {
		gs.Authorizations = append(gs.Authorizations, a)
		return false, nil
	}); err != nil {
		return nil, err
	}
	if err := k.Nodes.Walk(ctx, nil, func(_ string, n types.NodeState) (bool, error) {
		gs.Nodes = append(gs.Nodes, n)
		return false, nil
	}); err != nil {
		return nil, err
	}
	if err := k.Challenges.Walk(ctx, nil, func(key collections.Pair[uint64, string], rec types.ChallengeRecord) (bool, error) {
		dealID, slot, nodeID, err := parseChallengeID(key.K2())
		if err != nil {
			return false, err
		}
		gs.Challenges = append(gs.Challenges, types.GenesisChallenge{
			Epoch: key.K1(), NodeId: nodeID, DealId: dealID, Slot: slot, Record: rec,
		})
		return false, nil
	}); err != nil {
		return nil, err
	}
	if err := k.Rechallenge.Walk(ctx, nil, func(key collections.Pair[string, string]) (bool, error) {
		var dealID uint64
		var slot uint32
		if _, err := fmt.Sscanf(key.K2(), "%d/%d", &dealID, &slot); err != nil {
			return false, err
		}
		gs.Rechallenges = append(gs.Rechallenges, types.Rechallenge{NodeId: key.K1(), DealId: dealID, Slot: slot})
		return false, nil
	}); err != nil {
		return nil, err
	}
	if err := k.Queue.Walk(ctx, nil, func(_ uint64, s types.Settlement) (bool, error) {
		gs.Settlements = append(gs.Settlements, s)
		return false, nil
	}); err != nil {
		return nil, err
	}
	if err := k.EpochMinted.Walk(ctx, nil, func(epoch uint64, minted math.Int) (bool, error) {
		gs.EpochMints = append(gs.EpochMints, types.EpochMint{Epoch: epoch, Minted: minted})
		return false, nil
	}); err != nil {
		return nil, err
	}
	if err := k.OperatorMinted.Walk(ctx, nil, func(key collections.Pair[uint64, string], minted math.Int) (bool, error) {
		gs.OperatorMints = append(gs.OperatorMints, types.OperatorMint{Epoch: key.K1(), Operator: key.K2(), Minted: minted})
		return false, nil
	}); err != nil {
		return nil, err
	}
	if err := k.Releases.Walk(ctx, nil, func(key collections.Pair[uint64, string], count uint64) (bool, error) {
		gs.ReleaseCounts = append(gs.ReleaseCounts, types.ReleaseCount{Epoch: key.K1(), NodeId: key.K2(), Count: count})
		return false, nil
	}); err != nil {
		return nil, err
	}
	if err := k.Reserved.Walk(ctx, nil, func(nodeID string, bytes uint64) (bool, error) {
		gs.Reserved = append(gs.Reserved, types.Reserved{NodeId: nodeID, Bytes: bytes})
		return false, nil
	}); err != nil {
		return nil, err
	}
	return gs, nil
}
