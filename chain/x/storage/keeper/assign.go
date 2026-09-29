package keeper

import (
	"errors"
	"fmt"
	"sort"

	"cosmossdk.io/collections"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/storage/types"
)

func (k Keeper) assignDue(ctx sdk.Context) error {
	var ids []uint64
	if err := k.Pending.Walk(ctx, nil, func(id uint64) (bool, error) {
		ids = append(ids, id)
		return false, nil
	}); err != nil {
		return fmt.Errorf("failed to walk pending deals: %w", err)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		deal, err := k.loadDeal(ctx, id)
		if err != nil {
			return err
		}
		if deal.Status == types.DealStatus_DEAL_STATUS_REFUNDED || deal.Status == types.DealStatus_DEAL_STATUS_EXPIRED {
			if err := k.Pending.Remove(ctx, id); err != nil {
				return err
			}
			continue
		}
		if ctx.BlockHeight() < deal.AssignAtHeight {
			continue
		}
		if err := k.assignDeal(ctx, deal); err != nil {
			return err
		}
	}
	return nil
}

func (k Keeper) assignDeal(ctx sdk.Context, deal types.Deal) error {
	slots, err := k.dealSlots(ctx, deal.Id)
	if err != nil {
		return err
	}
	var open []int
	usedOp := map[string]struct{}{}
	usedNet := map[string]struct{}{}
	usedASN := map[uint32]struct{}{}
	held := 0
	for i := range slots {
		if slots[i].NodeId == "" {
			open = append(open, i)
			continue
		}
		held++
		usedOp[slots[i].Operator] = struct{}{}
		usedNet[slots[i].Network16] = struct{}{}
		usedASN[slots[i].Asn] = struct{}{}
	}
	if len(open) == 0 {
		return k.Pending.Remove(ctx, deal.Id)
	}
	prev := ctx.BlockHeader().LastBlockId.Hash
	var filled []int
	for _, idx := range open {
		slot := &slots[idx]
		seed := types.AssignmentSeed(prev, deal.Id, slot.Index)
		chosen, ok, err := k.pick(ctx, deal, slot.PieceBytes, slot.ExcludedOperator, seed, usedOp, usedNet, usedASN)
		if err != nil {
			return err
		}
		if !ok {
			for _, f := range filled {
				if err := k.detachSlot(ctx, &slots[f]); err != nil {
					return err
				}
				slots[f].Status = types.SlotStatus_SLOT_STATUS_UNASSIGNED
				if err := k.saveSlot(ctx, slots[f]); err != nil {
					return err
				}
			}
			if held == 0 && deal.Status == types.DealStatus_DEAL_STATUS_OPEN {
				return k.refundUnassigned(ctx, deal)
			}
			return nil
		}
		if err := k.bindSlot(ctx, deal, slot, chosen); err != nil {
			return err
		}
		if err := k.saveSlot(ctx, *slot); err != nil {
			return err
		}
		filled = append(filled, idx)
		usedOp[slot.Operator] = struct{}{}
		usedNet[slot.Network16] = struct{}{}
		usedASN[slot.Asn] = struct{}{}
		ctx.EventManager().EmitEvent(sdk.NewEvent("storage_slot_assigned",
			sdk.NewAttribute("deal_id", fmt.Sprintf("%d", deal.Id)),
			sdk.NewAttribute("slot", fmt.Sprintf("%d", slot.Index)),
			sdk.NewAttribute("node_id", slot.NodeId),
		))
	}
	return k.Pending.Remove(ctx, deal.Id)
}

func (k Keeper) pick(
	ctx sdk.Context,
	deal types.Deal,
	pieceBytes uint64,
	excluded string,
	seed []byte,
	usedOp map[string]struct{},
	usedNet map[string]struct{},
	usedASN map[uint32]struct{},
) (types.Candidate, bool, error) {
	cands, err := k.candidates(ctx)
	if err != nil {
		return types.Candidate{}, false, err
	}
	p, err := k.params(ctx)
	if err != nil {
		return types.Candidate{}, false, err
	}
	probNodes, err := k.counterMap(ctx, k.ProbationNode)
	if err != nil {
		return types.Candidate{}, false, err
	}
	probOps, err := k.counterMap(ctx, k.ProbationOp)
	if err != nil {
		return types.Candidate{}, false, err
	}
	probNets, err := k.counterMap(ctx, k.ProbationNet)
	if err != nil {
		return types.Candidate{}, false, err
	}
	probASN := map[uint32]uint32{}
	if err := k.ProbationASN.Walk(ctx, nil, func(asn uint32, v uint64) (bool, error) {
		probASN[asn] = uint32(v)
		return false, nil
	}); err != nil {
		return types.Candidate{}, false, err
	}
	rules := types.PickRules{
		PieceBytes:         pieceBytes,
		RepairOperator:     deal.RepairDelegate,
		ExcludedOperator:   excluded,
		Protocol:           deal.Protocol,
		UsedOperators:      usedOp,
		UsedNetworks:       usedNet,
		UsedASNs:           usedASN,
		ProbationNodes:     probNodes,
		ProbationOperators: probOps,
		ProbationNetworks:  probNets,
		ProbationASNs:      probASN,
		ProbationSlots:     p.ProbationSlots,
		ProbationOpCap:     p.ProbationOperatorCap,
		ProbationNetCap:    p.ProbationNetwork16Cap,
		ProbationASNCap:    p.ProbationAsnCap,
	}
	c, ok := types.PickCandidate(cands, seed, rules)
	return c, ok, nil
}

func (k Keeper) candidates(ctx sdk.Context) ([]types.Candidate, error) {
	var out []types.Candidate
	err := k.Nodes.Walk(ctx, nil, func(id string, state types.NodeState) (bool, error) {
		active, err := k.nodes.IsActive(ctx, id)
		if err != nil {
			return false, fmt.Errorf("failed to read active flag of %s: %w", id, err)
		}
		onProbation := state.Probation && !state.Graduated
		if !active && onProbation {
			if active, err = k.nodes.IsProbation(ctx, id); err != nil {
				return false, fmt.Errorf("failed to read probation status of %s: %w", id, err)
			}
		}
		op, err := k.nodes.Operator(ctx, id)
		if err != nil {
			return false, err
		}
		net, err := k.nodes.Network16(ctx, id)
		if err != nil {
			return false, err
		}
		asn, err := k.nodes.ASN(ctx, id)
		if err != nil {
			return false, err
		}
		declared, err := k.nodes.DeclaredCapacity(ctx, id)
		if err != nil {
			return false, err
		}
		reserved, err := k.reservedOf(ctx, id)
		if err != nil {
			return false, err
		}
		out = append(out, types.Candidate{
			ID:        id,
			Operator:  op,
			Network16: net,
			ASN:       asn,
			Probation: onProbation,
			Active:    active,
			Declared:  declared,
			Reserved:  reserved,
		})
		return false, nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (k Keeper) bindSlot(ctx sdk.Context, deal types.Deal, slot *types.Slot, c types.Candidate) error {
	slot.NodeId = c.ID
	// A new holder starts with no misses: the old holder's are not its own.
	slot.ConsecutiveMisses = 0
	slot.Operator = c.Operator
	slot.Network16 = c.Network16
	slot.Asn = c.ASN
	slot.Status = types.SlotStatus_SLOT_STATUS_ASSIGNED
	slot.Accepted = false
	slot.AssignHeight = ctx.BlockHeight()
	slot.ExcludedOperator = ""
	if err := k.adjustReserved(ctx, c.ID, int64(slot.PieceBytes)); err != nil {
		return err
	}
	if err := k.addReplica(ctx, slot); err != nil {
		return err
	}
	if deal.Protocol {
		state, err := k.Nodes.Get(ctx, c.ID)
		if err != nil {
			return err
		}
		if err := k.noteProbationAssign(ctx, state, c.Operator, c.Network16, c.ASN, 1); err != nil {
			return err
		}
	}
	return nil
}

// detachSlot frees a slot's node: replica index, reserved bytes, probation
// counters and any rechallenge. The piece commitment stays.
func (k Keeper) detachSlot(ctx sdk.Context, slot *types.Slot) error {
	if slot.NodeId == "" {
		slot.Accepted = false
		slot.ReplicaIndexed = false
		return nil
	}
	nodeID := slot.NodeId
	op := slot.Operator
	net := slot.Network16
	asn := slot.Asn
	if err := k.removeReplica(ctx, slot); err != nil {
		return err
	}
	if slot.PieceBytes > 0 {
		if err := k.adjustReserved(ctx, nodeID, -int64(slot.PieceBytes)); err != nil {
			return err
		}
	}
	deal, err := k.loadDeal(ctx, slot.DealId)
	if err != nil {
		return err
	}
	if deal.Protocol {
		state, err := k.Nodes.Get(ctx, nodeID)
		if err != nil && !errors.Is(err, collections.ErrNotFound) {
			return err
		}
		if err == nil {
			if err := k.noteProbationAssign(ctx, state, op, net, asn, -1); err != nil {
				return err
			}
		}
	}
	if err := k.Rechallenge.Remove(ctx, collections.Join(nodeID, rechallengeID(slot.DealId, slot.Index))); err != nil {
		return err
	}
	slot.NodeId = ""
	slot.Operator = ""
	slot.Network16 = ""
	slot.Asn = 0
	slot.Accepted = false
	return nil
}

// AcceptDeal records the hot key's acceptance and opens this epoch's challenge.
func (k Keeper) AcceptDeal(ctx sdk.Context, msg *types.MsgAcceptDeal) error {
	if err := msg.ValidateBasic(); err != nil {
		return err
	}
	if err := k.requireHotKey(ctx, msg.NodeId, msg.Signer); err != nil {
		return err
	}
	slot, err := k.loadSlot(ctx, msg.DealId, msg.Slot)
	if err != nil {
		return err
	}
	if slot.NodeId != msg.NodeId || slot.Status != types.SlotStatus_SLOT_STATUS_ASSIGNED || slot.Accepted {
		return fmt.Errorf("slot %d/%d is not waiting for acceptance by %s", msg.DealId, msg.Slot, msg.NodeId)
	}
	p, err := k.params(ctx)
	if err != nil {
		return err
	}
	if ctx.BlockHeight() > slot.AssignHeight+int64(p.AcceptWindowBlocks) {
		return fmt.Errorf("accept window for slot %d/%d has closed", msg.DealId, msg.Slot)
	}
	slot.Accepted = true
	slot.Status = types.SlotStatus_SLOT_STATUS_ACTIVE
	if err := k.saveSlot(ctx, slot); err != nil {
		return err
	}
	deal, err := k.loadDeal(ctx, msg.DealId)
	if err != nil {
		return err
	}
	if deal.Status == types.DealStatus_DEAL_STATUS_OPEN {
		deal.Status = types.DealStatus_DEAL_STATUS_ACTIVE
		if err := k.saveDeal(ctx, deal); err != nil {
			return err
		}
	}
	epoch, err := k.currentEpoch(ctx)
	if err != nil {
		return err
	}
	if err := k.openSlotChallenge(ctx, epoch, slot); err != nil {
		return err
	}
	ctx.EventManager().EmitEvent(sdk.NewEvent("storage_slot_accepted",
		sdk.NewAttribute("deal_id", fmt.Sprintf("%d", msg.DealId)),
		sdk.NewAttribute("slot", fmt.Sprintf("%d", msg.Slot)),
		sdk.NewAttribute("node_id", msg.NodeId),
	))
	return nil
}

// DeclineDeal reassigns the slot. It does not slash.
func (k Keeper) DeclineDeal(ctx sdk.Context, msg *types.MsgDeclineDeal) error {
	if err := msg.ValidateBasic(); err != nil {
		return err
	}
	if err := k.requireHotKey(ctx, msg.NodeId, msg.Signer); err != nil {
		return err
	}
	slot, err := k.loadSlot(ctx, msg.DealId, msg.Slot)
	if err != nil {
		return err
	}
	if slot.NodeId != msg.NodeId || slot.Accepted || slot.Status != types.SlotStatus_SLOT_STATUS_ASSIGNED {
		return fmt.Errorf("slot %d/%d is not assigned to %s awaiting a response", msg.DealId, msg.Slot, msg.NodeId)
	}
	op := slot.Operator
	if err := k.detachSlot(ctx, &slot); err != nil {
		return err
	}
	slot.ExcludedOperator = op
	slot.Status = types.SlotStatus_SLOT_STATUS_UNASSIGNED
	slot.ConsecutiveMisses = 0
	if err := k.saveSlot(ctx, slot); err != nil {
		return err
	}
	deal, err := k.loadDeal(ctx, msg.DealId)
	if err != nil {
		return err
	}
	deal.AssignAtHeight = ctx.BlockHeight() + 1
	if err := k.saveDeal(ctx, deal); err != nil {
		return err
	}
	if err := k.Pending.Set(ctx, deal.Id); err != nil {
		return err
	}
	ctx.EventManager().EmitEvent(sdk.NewEvent("storage_slot_declined",
		sdk.NewAttribute("deal_id", fmt.Sprintf("%d", msg.DealId)),
		sdk.NewAttribute("slot", fmt.Sprintf("%d", msg.Slot)),
		sdk.NewAttribute("node_id", msg.NodeId),
	))
	return nil
}

// ReleaseReplica drops a replica for a legal reason. It is rate-limited and
// does not slash. The slot is reassigned.
func (k Keeper) ReleaseReplica(ctx sdk.Context, msg *types.MsgReleaseReplica) error {
	if err := msg.ValidateBasic(); err != nil {
		return err
	}
	if err := k.requireHotKey(ctx, msg.NodeId, msg.Signer); err != nil {
		return err
	}
	p, err := k.params(ctx)
	if err != nil {
		return err
	}
	epoch, err := k.currentEpoch(ctx)
	if err != nil {
		return err
	}
	count, err := k.Releases.Get(ctx, collections.Join(epoch, msg.NodeId))
	if err != nil && !errors.Is(err, collections.ErrNotFound) {
		return err
	}
	if count >= p.MaxReleasesPerEpoch {
		return fmt.Errorf("node %s already released %d replicas this epoch", msg.NodeId, count)
	}
	slot, err := k.loadSlot(ctx, msg.DealId, msg.Slot)
	if err != nil {
		return err
	}
	if slot.NodeId != msg.NodeId {
		return fmt.Errorf("slot %d/%d is not held by %s", msg.DealId, msg.Slot, msg.NodeId)
	}
	op := slot.Operator
	if err := k.detachSlot(ctx, &slot); err != nil {
		return err
	}
	slot.ExcludedOperator = op
	slot.Status = types.SlotStatus_SLOT_STATUS_UNASSIGNED
	slot.ConsecutiveMisses = 0
	if err := k.saveSlot(ctx, slot); err != nil {
		return err
	}
	if err := k.Releases.Set(ctx, collections.Join(epoch, msg.NodeId), count+1); err != nil {
		return err
	}
	deal, err := k.loadDeal(ctx, msg.DealId)
	if err != nil {
		return err
	}
	deal.AssignAtHeight = ctx.BlockHeight() + 1
	if err := k.saveDeal(ctx, deal); err != nil {
		return err
	}
	return k.Pending.Set(ctx, deal.Id)
}

func (k Keeper) expireAcceptWindows(ctx sdk.Context) error {
	p, err := k.params(ctx)
	if err != nil {
		return err
	}
	var stale []types.Slot
	err = k.Slots.Walk(ctx, nil, func(_ collections.Pair[uint64, uint32], slot types.Slot) (bool, error) {
		if slot.Status == types.SlotStatus_SLOT_STATUS_ASSIGNED && !slot.Accepted &&
			ctx.BlockHeight() > slot.AssignHeight+int64(p.AcceptWindowBlocks) {
			stale = append(stale, slot)
		}
		return false, nil
	})
	if err != nil {
		return err
	}
	for _, slot := range stale {
		op := slot.Operator
		if err := k.detachSlot(ctx, &slot); err != nil {
			return err
		}
		slot.ExcludedOperator = op
		slot.Status = types.SlotStatus_SLOT_STATUS_UNASSIGNED
		if err := k.saveSlot(ctx, slot); err != nil {
			return err
		}
		deal, err := k.loadDeal(ctx, slot.DealId)
		if err != nil {
			return err
		}
		deal.AssignAtHeight = ctx.BlockHeight() + 1
		if err := k.saveDeal(ctx, deal); err != nil {
			return err
		}
		if err := k.Pending.Set(ctx, deal.Id); err != nil {
			return err
		}
	}
	return nil
}
