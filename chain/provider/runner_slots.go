package provider

import (
	"context"
	"fmt"

	"github.com/DeBrosOfficial/network/chain/x/storage/types"
)

// settlePending accepts each waiting slot whose piece arrived, declines one
// whose accept window is about to close, and forgets one the chain no longer
// assigns to this node.
func (r *Runner) settlePending(ctx context.Context, latest int64, params types.Params) error {
	for _, p := range r.pendingCopy() {
		slot, err := r.chain.Slot(ctx, p.DealID, p.Slot)
		if err != nil {
			return err
		}
		if slot.NodeId != r.nodeID || slot.Status != types.SlotStatus_SLOT_STATUS_ASSIGNED || slot.Accepted {
			if err := r.dropPending(p.DealID, p.Slot); err != nil {
				return err
			}
			continue
		}
		if err := r.notePendingRoot(p.DealID, p.Slot, rootName(slot.PieceRoot)); err != nil {
			return err
		}
		if err := r.decide(ctx, latest, params, slot); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runner) decide(ctx context.Context, latest int64, params types.Params, slot types.Slot) error {
	closes := slot.AssignHeight + int64(params.AcceptWindowBlocks)
	_, stored, err := r.store.FindByRoot(slot.PieceRoot)
	if err != nil {
		return err
	}
	if !stored && latest < closes-DeclineMarginBlocks {
		return nil
	}
	accept, decline, err := r.store.Decide(r.signer, r.nodeID, slot.DealId, slot.Index, slot.PieceRoot)
	if err != nil {
		return err
	}
	if accept != nil {
		if err := r.chain.Submit(ctx, accept); err != nil {
			return fmt.Errorf("accept deal %d slot %d: %w", slot.DealId, slot.Index, err)
		}
	} else {
		if err := r.chain.Submit(ctx, decline); err != nil {
			return fmt.Errorf("decline deal %d slot %d: %w", slot.DealId, slot.Index, err)
		}
	}
	return r.dropPending(slot.DealId, slot.Index)
}

// answer proves every unproved challenge this node holds for epoch, in
// batches of MaxProofsPerTx. It returns how many challenges have no local
// piece. A proof tx that is dropped is rebuilt on the next step, because the
// chain still lists the challenge as unproved.
func (r *Runner) answer(ctx context.Context, epoch uint64) (int, error) {
	challenges, err := r.chain.Challenges(ctx, epoch, r.nodeID)
	if err != nil {
		return 0, err
	}
	proofs, missing, err := r.store.AnswerChallenges(epoch, r.nodeID, challenges)
	if err != nil {
		return 0, err
	}
	for start := 0; start < len(proofs); start += MaxProofsPerTx {
		end := min(start+MaxProofsPerTx, len(proofs))
		msg := &types.MsgSubmitProofs{Signer: r.signer, NodeId: r.nodeID, Proofs: proofs[start:end]}
		if err := r.chain.Submit(ctx, msg); err != nil {
			return 0, fmt.Errorf("submit %d proofs for epoch %d: %w", end-start, epoch, err)
		}
	}
	return len(missing), nil
}

// sweep releases a bound slot once the chain has stopped naming this node
// for it for ReleaseGraceEpochs. Eviction, expiry and a reassignment after a
// missed accept window all show up here as a slot that is no longer ours.
func (r *Runner) sweep(ctx context.Context, epoch uint64) error {
	bound, err := r.store.Assignments()
	if err != nil {
		return err
	}
	for _, a := range bound {
		slot, err := r.chain.Slot(ctx, a.DealID, a.Slot)
		if err != nil {
			return err
		}
		if holds(slot, r.nodeID) {
			r.clearGone(a.DealID, a.Slot)
			continue
		}
		if epoch < r.markGone(a.DealID, a.Slot, epoch)+ReleaseGraceEpochs {
			continue
		}
		if err := r.store.Release(a.DealID, a.Slot); err != nil {
			return err
		}
		r.clearGone(a.DealID, a.Slot)
	}
	return r.save()
}

func holds(slot types.Slot, nodeID string) bool {
	if slot.NodeId != nodeID {
		return false
	}
	return slot.Status == types.SlotStatus_SLOT_STATUS_ASSIGNED || slot.Status == types.SlotStatus_SLOT_STATUS_ACTIVE
}
