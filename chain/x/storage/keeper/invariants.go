package keeper

import (
	"fmt"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/storage/types"
)

// StorageInvariants is the result of CheckInvariants.
type StorageInvariants struct {
	EscrowConserved        bool
	SubsidyWithinCeiling   bool
	DistinctOperators      bool
	ReservedWithinDeclared bool
	QueueWellFormed        bool
	Detail                 string
}

// CheckInvariants checks the C7 invariants:
// escrow conservation, subsidy minted <= ceiling, distinct operators per deal,
// reserved <= declared capacity, and a well-formed settlement queue.
func (k Keeper) CheckInvariants(ctx sdk.Context) (StorageInvariants, error) {
	escrowSum := math.ZeroInt()
	distinct := true
	var distinctDetail string
	err := k.Deals.Walk(ctx, nil, func(id uint64, deal types.Deal) (bool, error) {
		if deal.Escrow.IsNil() {
			return false, fmt.Errorf("deal %d escrow is unset", id)
		}
		escrowSum = escrowSum.Add(deal.Escrow)
		slots, err := k.dealSlots(ctx, id)
		if err != nil {
			return false, err
		}
		ops := map[string]struct{}{}
		nets := map[string]struct{}{}
		asns := map[uint32]struct{}{}
		for _, slot := range slots {
			if slot.NodeId == "" {
				continue
			}
			if _, ok := ops[slot.Operator]; ok {
				distinct = false
				distinctDetail = fmt.Sprintf("deal %d repeats operator %s", id, slot.Operator)
			}
			ops[slot.Operator] = struct{}{}
			if deal.Protocol {
				if _, ok := nets[slot.Network16]; ok {
					distinct = false
					distinctDetail = fmt.Sprintf("protocol deal %d repeats /16 %s", id, slot.Network16)
				}
				if _, ok := asns[slot.Asn]; ok {
					distinct = false
					distinctDetail = fmt.Sprintf("protocol deal %d repeats ASN %d", id, slot.Asn)
				}
				nets[slot.Network16] = struct{}{}
				asns[slot.Asn] = struct{}{}
			}
		}
		return false, nil
	})
	if err != nil {
		return StorageInvariants{}, err
	}
	escrowBal := k.moduleBalance(ctx, types.EscrowModuleName)
	escrowOK := escrowSum.Equal(escrowBal)

	subsidyOK := true
	var subsidyDetail string
	pendingMint := map[uint64]math.Int{}
	if err := k.Queue.Walk(ctx, nil, func(_ uint64, item types.Settlement) (bool, error) {
		cur := pendingMint[item.Epoch]
		if cur.IsNil() {
			cur = math.ZeroInt()
		}
		if !item.MintPay.IsNil() {
			pendingMint[item.Epoch] = cur.Add(item.MintPay)
		}
		return false, nil
	}); err != nil {
		return StorageInvariants{}, err
	}
	seenEpoch := map[uint64]struct{}{}
	if err := k.EpochMinted.Walk(ctx, nil, func(epoch uint64, minted math.Int) (bool, error) {
		seenEpoch[epoch] = struct{}{}
		ceiling, err := k.emission.StorageCeiling(ctx, epoch)
		if err != nil {
			return false, err
		}
		if ceiling.IsNil() {
			ceiling = math.ZeroInt()
		}
		pending := pendingMint[epoch]
		if pending.IsNil() {
			pending = math.ZeroInt()
		}
		if minted.Add(pending).GT(ceiling) {
			subsidyOK = false
			subsidyDetail = fmt.Sprintf("epoch %d minted %s + pending %s exceeds ceiling %s", epoch, minted, pending, ceiling)
		}
		return false, nil
	}); err != nil {
		return StorageInvariants{}, err
	}
	for epoch, pending := range pendingMint {
		if _, ok := seenEpoch[epoch]; ok {
			continue
		}
		ceiling, err := k.emission.StorageCeiling(ctx, epoch)
		if err != nil {
			return StorageInvariants{}, err
		}
		if ceiling.IsNil() {
			ceiling = math.ZeroInt()
		}
		if pending.GT(ceiling) {
			subsidyOK = false
			subsidyDetail = fmt.Sprintf("epoch %d pending mint %s exceeds ceiling %s", epoch, pending, ceiling)
		}
	}

	reservedOK := true
	var reservedDetail string
	err = k.Reserved.Walk(ctx, nil, func(nodeID string, reserved uint64) (bool, error) {
		declared, err := k.nodes.DeclaredCapacity(ctx, nodeID)
		if err != nil {
			return false, err
		}
		if reserved > declared {
			reservedOK = false
			reservedDetail = fmt.Sprintf("node %s reserved %d exceeds declared %d", nodeID, reserved, declared)
		}
		return false, nil
	})
	if err != nil {
		return StorageInvariants{}, err
	}

	queueOK, queueDetail, err := k.queueWellFormed(ctx)
	if err != nil {
		return StorageInvariants{}, err
	}

	detail := fmt.Sprintf(
		"escrow conserved: %t (ledger=%s module=%s)\nsubsidy within ceiling: %t %s\ndistinct operators: %t %s\nreserved within declared: %t %s\nqueue well formed: %t %s\n",
		escrowOK, escrowSum, escrowBal, subsidyOK, subsidyDetail, distinct, distinctDetail, reservedOK, reservedDetail, queueOK, queueDetail,
	)
	return StorageInvariants{
		EscrowConserved:        escrowOK,
		SubsidyWithinCeiling:   subsidyOK,
		DistinctOperators:      distinct,
		ReservedWithinDeclared: reservedOK,
		QueueWellFormed:        queueOK,
		Detail:                 detail,
	}, nil
}

func (k Keeper) queueWellFormed(ctx sdk.Context) (bool, string, error) {
	head, err := k.QueueHead.Get(ctx)
	if err != nil {
		return false, "", err
	}
	tail, err := k.QueueTail.Get(ctx)
	if err != nil {
		return false, "", err
	}
	if head > tail {
		return false, fmt.Sprintf("head %d > tail %d", head, tail), nil
	}
	for seq := head; seq < tail; seq++ {
		has, err := k.Queue.Has(ctx, seq)
		if err != nil {
			return false, "", err
		}
		if !has {
			return false, fmt.Sprintf("missing settlement %d", seq), nil
		}
	}
	// Nothing is queued past the tail.
	var extra bool
	if err := k.Queue.Walk(ctx, nil, func(seq uint64, _ types.Settlement) (bool, error) {
		if seq < head || seq >= tail {
			extra = true
			return true, nil
		}
		return false, nil
	}); err != nil {
		return false, "", err
	}
	if extra {
		return false, "queue contains a sequence outside [head, tail)", nil
	}
	return true, "", nil
}
