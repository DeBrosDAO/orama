package keeper

import (
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/storage/types"
)

func coin(amount math.Int) sdk.Coin {
	return sdk.NewCoin(params.BaseDenom, amount)
}

func coins(amount math.Int) sdk.Coins {
	return sdk.NewCoins(coin(amount))
}

func parseAddr(bech32 string) (sdk.AccAddress, error) {
	addr, err := sdk.AccAddressFromBech32(bech32)
	if err != nil {
		return nil, rejectf("invalid address %q: %w", bech32, err)
	}
	return addr, nil
}

func challengeID(nodeID string, dealID uint64, slot uint32) string {
	return fmt.Sprintf("%s|%d|%d", nodeID, dealID, slot)
}

func rechallengeID(dealID uint64, slot uint32) string {
	return fmt.Sprintf("%d/%d", dealID, slot)
}

func probationDepositID(nodeID string) string {
	return "storage/probation/" + nodeID
}

func (k Keeper) loadDeal(ctx sdk.Context, id uint64) (types.Deal, error) {
	deal, err := k.Deals.Get(ctx, id)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return types.Deal{}, rejectf("deal %d does not exist", id)
		}
		return types.Deal{}, fmt.Errorf("failed to load deal %d: %w", id, err)
	}
	return deal, nil
}

func (k Keeper) saveDeal(ctx sdk.Context, deal types.Deal) error {
	old, err := k.Deals.Get(ctx, deal.Id)
	switch {
	case err == nil:
		if old.EndEpoch != deal.EndEpoch {
			if err := k.DealExpiry.Remove(ctx, collections.Join(old.EndEpoch, deal.Id)); err != nil {
				return fmt.Errorf("failed to re-index deal %d: %w", deal.Id, err)
			}
		}
	case !errors.Is(err, collections.ErrNotFound):
		return fmt.Errorf("failed to read deal %d: %w", deal.Id, err)
	}
	if err := k.DealExpiry.Set(ctx, collections.Join(deal.EndEpoch, deal.Id)); err != nil {
		return fmt.Errorf("failed to index deal %d: %w", deal.Id, err)
	}
	if err := k.Deals.Set(ctx, deal.Id, deal); err != nil {
		return fmt.Errorf("failed to store deal %d: %w", deal.Id, err)
	}
	return nil
}

// deleteDeal removes a finished deal, its slots and its index rows. The caller has already detached every
// slot and returned the escrow, and no settlement row of the deal is queued.
func (k Keeper) deleteDeal(ctx sdk.Context, deal types.Deal) error {
	slots, err := k.dealSlots(ctx, deal.Id)
	if err != nil {
		return err
	}
	for _, slot := range slots {
		if slot.NodeId != "" || slot.ReplicaIndexed {
			return fmt.Errorf("deal %d slot %d is still held by %q and cannot be deleted", deal.Id, slot.Index, slot.NodeId)
		}
		if err := k.AcceptWindow.Remove(ctx, acceptWindowKey(slot)); err != nil {
			return err
		}
		if err := k.Slots.Remove(ctx, collections.Join(slot.DealId, slot.Index)); err != nil {
			return fmt.Errorf("failed to delete deal %d slot %d: %w", slot.DealId, slot.Index, err)
		}
	}
	if err := k.DealExpiry.Remove(ctx, collections.Join(deal.EndEpoch, deal.Id)); err != nil {
		return err
	}
	if err := k.Pending.Remove(ctx, deal.Id); err != nil {
		return err
	}
	if err := k.Deals.Remove(ctx, deal.Id); err != nil {
		return fmt.Errorf("failed to delete deal %d: %w", deal.Id, err)
	}
	return nil
}

func (k Keeper) loadSlot(ctx sdk.Context, dealID uint64, index uint32) (types.Slot, error) {
	slot, err := k.Slots.Get(ctx, collections.Join(dealID, index))
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return types.Slot{}, rejectf("deal %d slot %d does not exist", dealID, index)
		}
		return types.Slot{}, fmt.Errorf("failed to load deal %d slot %d: %w", dealID, index, err)
	}
	return slot, nil
}

func (k Keeper) saveSlot(ctx sdk.Context, slot types.Slot) error {
	old, err := k.Slots.Get(ctx, collections.Join(slot.DealId, slot.Index))
	switch {
	case err == nil:
		if err := k.AcceptWindow.Remove(ctx, acceptWindowKey(old)); err != nil {
			return fmt.Errorf("failed to re-index deal %d slot %d: %w", slot.DealId, slot.Index, err)
		}
	case !errors.Is(err, collections.ErrNotFound):
		return fmt.Errorf("failed to read deal %d slot %d: %w", slot.DealId, slot.Index, err)
	}
	if slot.Status == types.SlotStatus_SLOT_STATUS_ASSIGNED && !slot.Accepted {
		if err := k.AcceptWindow.Set(ctx, acceptWindowKey(slot)); err != nil {
			return fmt.Errorf("failed to index deal %d slot %d: %w", slot.DealId, slot.Index, err)
		}
	}
	if err := k.Slots.Set(ctx, collections.Join(slot.DealId, slot.Index), slot); err != nil {
		return fmt.Errorf("failed to store deal %d slot %d: %w", slot.DealId, slot.Index, err)
	}
	return nil
}

func acceptWindowKey(slot types.Slot) collections.Pair[uint64, collections.Pair[uint64, uint32]] {
	return collections.Join(uint64(slot.AssignHeight), collections.Join(slot.DealId, slot.Index))
}

func (k Keeper) dealSlots(ctx sdk.Context, dealID uint64) ([]types.Slot, error) {
	var slots []types.Slot
	err := k.Slots.Walk(ctx, collections.NewPrefixedPairRange[uint64, uint32](dealID), func(_ collections.Pair[uint64, uint32], slot types.Slot) (bool, error) {
		slots = append(slots, slot)
		return false, nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to walk slots of deal %d: %w", dealID, err)
	}
	return slots, nil
}

func (k Keeper) requireHotKey(ctx sdk.Context, nodeID, signer string) error {
	signerAddr, err := parseAddr(signer)
	if err != nil {
		return err
	}
	hot, err := k.nodes.HotKey(ctx, nodeID)
	if err != nil {
		return fmt.Errorf("failed to read hot key of %s: %w", nodeID, err)
	}
	if !hot.Equals(signerAddr) {
		return fmt.Errorf("signer %s is not the hot key of node %s", signer, nodeID)
	}
	return nil
}

func (k Keeper) moduleBalance(ctx sdk.Context, moduleName string) math.Int {
	return k.bank.GetBalance(ctx, authtypes.NewModuleAddress(moduleName), params.BaseDenom).Amount
}

// replicasHeld is how many replicas nodeID currently holds.
func (k Keeper) replicasHeld(ctx sdk.Context, nodeID string) (uint64, error) {
	n, err := k.ReplicaCount.Get(ctx, nodeID)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return 0, nil
		}
		return 0, fmt.Errorf("failed to read replica count of %s: %w", nodeID, err)
	}
	return n, nil
}

func (k Keeper) addReplica(ctx sdk.Context, slot *types.Slot) error {
	count, err := k.ReplicaCount.Get(ctx, slot.NodeId)
	if err != nil && !errors.Is(err, collections.ErrNotFound) {
		return fmt.Errorf("failed to load replica count of %s: %w", slot.NodeId, err)
	}
	ref := types.SlotRef{DealId: slot.DealId, Slot: slot.Index}
	if err := k.ReplicaAt.Set(ctx, collections.Join(slot.NodeId, count), ref); err != nil {
		return fmt.Errorf("failed to index replica: %w", err)
	}
	if err := k.ReplicaCount.Set(ctx, slot.NodeId, count+1); err != nil {
		return fmt.Errorf("failed to store replica count: %w", err)
	}
	slot.ReplicaIndexed = true
	slot.ReplicaSeq = count
	return nil
}

func (k Keeper) removeReplica(ctx sdk.Context, slot *types.Slot) error {
	if !slot.ReplicaIndexed || slot.NodeId == "" {
		slot.ReplicaIndexed = false
		return nil
	}
	count, err := k.ReplicaCount.Get(ctx, slot.NodeId)
	if err != nil {
		return fmt.Errorf("failed to load replica count of %s: %w", slot.NodeId, err)
	}
	if count == 0 || slot.ReplicaSeq >= count {
		return fmt.Errorf("node %s replica seq %d is outside count %d", slot.NodeId, slot.ReplicaSeq, count)
	}
	last := count - 1
	if slot.ReplicaSeq != last {
		moved, err := k.ReplicaAt.Get(ctx, collections.Join(slot.NodeId, last))
		if err != nil {
			return fmt.Errorf("failed to load last replica of %s: %w", slot.NodeId, err)
		}
		if err := k.ReplicaAt.Set(ctx, collections.Join(slot.NodeId, slot.ReplicaSeq), moved); err != nil {
			return err
		}
		movedSlot, err := k.loadSlot(ctx, moved.DealId, moved.Slot)
		if err != nil {
			return err
		}
		movedSlot.ReplicaSeq = slot.ReplicaSeq
		if err := k.saveSlot(ctx, movedSlot); err != nil {
			return err
		}
	}
	if err := k.ReplicaAt.Remove(ctx, collections.Join(slot.NodeId, last)); err != nil {
		return err
	}
	if last == 0 {
		if err := k.ReplicaCount.Remove(ctx, slot.NodeId); err != nil {
			return err
		}
	} else if err := k.ReplicaCount.Set(ctx, slot.NodeId, last); err != nil {
		return err
	}
	slot.ReplicaIndexed = false
	slot.ReplicaSeq = 0
	return nil
}

func (k Keeper) bumpCounter(ctx sdk.Context, m collections.Map[string, uint64], key string, delta int64) error {
	cur, err := m.Get(ctx, key)
	if err != nil && !errors.Is(err, collections.ErrNotFound) {
		return err
	}
	next, err := applyDelta(cur, delta)
	if err != nil {
		return fmt.Errorf("counter %s: %w", key, err)
	}
	if next == 0 {
		return m.Remove(ctx, key)
	}
	return m.Set(ctx, key, next)
}

func (k Keeper) bumpASN(ctx sdk.Context, asn uint32, delta int64) error {
	cur, err := k.ProbationASN.Get(ctx, asn)
	if err != nil && !errors.Is(err, collections.ErrNotFound) {
		return err
	}
	next, err := applyDelta(cur, delta)
	if err != nil {
		return err
	}
	if next == 0 {
		return k.ProbationASN.Remove(ctx, asn)
	}
	return k.ProbationASN.Set(ctx, asn, next)
}

func applyDelta(cur uint64, delta int64) (uint64, error) {
	if delta >= 0 {
		return cur + uint64(delta), nil
	}
	sub := uint64(-delta)
	if cur < sub {
		return 0, fmt.Errorf("counter %d cannot drop by %d", cur, sub)
	}
	return cur - sub, nil
}

func (k Keeper) noteProbationAssign(ctx sdk.Context, node types.NodeState, operator, network string, asn uint32, delta int64) error {
	if !node.Probation || node.Graduated {
		return nil
	}
	if err := k.bumpCounter(ctx, k.ProbationNode, node.NodeId, delta); err != nil {
		return err
	}
	if err := k.bumpCounter(ctx, k.ProbationOp, operator, delta); err != nil {
		return err
	}
	if err := k.bumpCounter(ctx, k.ProbationNet, network, delta); err != nil {
		return err
	}
	return k.bumpASN(ctx, asn, delta)
}

func (k Keeper) counterMap(ctx sdk.Context, m collections.Map[string, uint64]) (map[string]uint32, error) {
	out := map[string]uint32{}
	err := m.Walk(ctx, nil, func(key string, v uint64) (bool, error) {
		out[key] = uint32(v)
		return false, nil
	})
	return out, err
}

func (k Keeper) queuePending(ctx sdk.Context, dealID uint64) (uint64, error) {
	v, err := k.QueuePending.Get(ctx, dealID)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return 0, nil
		}
		return 0, err
	}
	return v, nil
}

func (k Keeper) addQueuePending(ctx sdk.Context, dealID uint64, delta int64) error {
	cur, err := k.queuePending(ctx, dealID)
	if err != nil {
		return err
	}
	next, err := applyDelta(cur, delta)
	if err != nil {
		return err
	}
	if next == 0 {
		return k.QueuePending.Remove(ctx, dealID)
	}
	return k.QueuePending.Set(ctx, dealID, next)
}

// ArchiveDealActive reports whether dealID is an ARCHIVE deal that is active.
// An unknown deal is not an error; it is simply not an active ARCHIVE deal.
func (k Keeper) ArchiveDealActive(ctx sdk.Context, dealID uint64) (bool, error) {
	deal, err := k.Deals.Get(ctx, dealID)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("failed to load deal %d: %w", dealID, err)
	}
	return deal.Class == types.DealClass_DEAL_CLASS_ARCHIVE && deal.Status == types.DealStatus_DEAL_STATUS_ACTIVE, nil
}

// ArchiveDealLive reports whether dealID is an ARCHIVE deal that is still running: OPEN, waiting
// for its first provider, or ACTIVE. An unknown deal is not an error; it is not live.
func (k Keeper) ArchiveDealLive(ctx sdk.Context, dealID uint64) (bool, error) {
	deal, err := k.Deals.Get(ctx, dealID)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("failed to load deal %d: %w", dealID, err)
	}
	running := deal.Status == types.DealStatus_DEAL_STATUS_OPEN || deal.Status == types.DealStatus_DEAL_STATUS_ACTIVE
	return deal.Class == types.DealClass_DEAL_CLASS_ARCHIVE && running, nil
}
