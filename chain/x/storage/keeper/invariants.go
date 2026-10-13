package keeper

import (
	"fmt"

	"cosmossdk.io/collections"
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
// reserved <= declared capacity with x/nodes' reservation equal to the bytes of the replicas
// x/storage holds, and a well-formed settlement queue.
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
	// Every queued mint payment was minted into the storage module account
	// when its epoch closed, and x/emission refused anything past that
	// epoch's ceiling then. The account holds exactly what is still owed.
	owed := math.ZeroInt()
	// nosemgrep: consensus-map-iteration -- sums exact integers in a query-only invariant check
	for _, pending := range pendingMint {
		owed = owed.Add(pending)
	}
	if held := k.moduleBalance(ctx, types.ModuleName); !held.Equal(owed) {
		subsidyOK = false
		subsidyDetail = fmt.Sprintf("storage account holds %s but queued mint payments are %s", held, owed)
	}

	reservedOK, reservedDetail, err := k.reservedMatchesReplicas(ctx)
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

// reservedMatchesReplicas checks, for every node, that the bytes x/nodes has reserved are exactly the
// piece bytes of the replicas x/storage indexes on it, and that they fit the declared capacity. x/nodes
// owns the counter and x/storage moves it only as it binds and detaches a slot, so a difference means
// one of the two moved without the other.
func (k Keeper) reservedMatchesReplicas(ctx sdk.Context) (bool, string, error) {
	sums := map[string]uint64{}
	var order []string
	err := k.ReplicaAt.Walk(ctx, nil, func(key collections.Pair[string, uint64], ref types.SlotRef) (bool, error) {
		slot, err := k.loadSlot(ctx, ref.DealId, ref.Slot)
		if err != nil {
			return false, err
		}
		if _, seen := sums[key.K1()]; !seen {
			order = append(order, key.K1())
		}
		sums[key.K1()] += slot.PieceBytes
		return false, nil
	})
	if err != nil {
		return false, "", fmt.Errorf("failed to sum replica bytes: %w", err)
	}
	ok, detail := true, ""
	check := func(nodeID string, held uint64) error {
		reserved, err := k.nodes.ReservedCapacity(ctx, nodeID)
		if err != nil {
			return fmt.Errorf("failed to read reserved capacity of %s: %w", nodeID, err)
		}
		declared, err := k.nodes.DeclaredCapacity(ctx, nodeID)
		if err != nil {
			return fmt.Errorf("failed to read declared capacity of %s: %w", nodeID, err)
		}
		if reserved != held {
			ok, detail = false, fmt.Sprintf("node %s reserved %d but its replicas hold %d", nodeID, reserved, held)
		}
		if reserved > declared {
			ok, detail = false, fmt.Sprintf("node %s reserved %d exceeds declared %d", nodeID, reserved, declared)
		}
		return nil
	}
	for _, nodeID := range order {
		if err := check(nodeID, sums[nodeID]); err != nil {
			return false, "", err
		}
	}
	err = k.Nodes.Walk(ctx, nil, func(nodeID string, _ types.NodeState) (bool, error) {
		if _, held := sums[nodeID]; held {
			return false, nil
		}
		return false, check(nodeID, 0)
	})
	if err != nil {
		return false, "", err
	}
	return ok, detail, nil
}
