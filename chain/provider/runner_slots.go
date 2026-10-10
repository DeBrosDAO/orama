package provider

import (
	"context"
	"errors"
	"fmt"

	"github.com/DeBrosOfficial/network/chain/x/storage/types"
)

// settlePending accepts each waiting slot whose piece arrived, declines one
// whose accept window is about to close, and forgets one the chain no longer
// assigns to this node. A slot that fails is reported and the rest still run.
func (r *Runner) settlePending(ctx context.Context, latest int64, params types.Params) error {
	var errs []error
	errs = append(errs, r.noteClasses(ctx))
	for _, p := range r.pendingCopy() {
		if err := r.settleOne(ctx, latest, params, p); err != nil {
			errs = append(errs, fmt.Errorf("deal %d slot %d: %w", p.DealID, p.Slot, err))
		}
	}
	return errors.Join(errs...)
}

func (r *Runner) settleOne(ctx context.Context, latest int64, params types.Params, p pendingSlot) error {
	slot, err := r.chain.Slot(ctx, p.DealID, p.Slot)
	if err != nil {
		return err
	}
	if slot.NodeId != r.nodeID || slot.Status != types.SlotStatus_SLOT_STATUS_ASSIGNED || slot.Accepted {
		return r.dropPending(p.DealID, p.Slot)
	}
	if err := r.notePendingRoot(p.DealID, p.Slot, rootName(slot.PieceRoot)); err != nil {
		return err
	}
	return r.decide(ctx, latest, params, slot)
}

// noteClasses reads the deal class of every waiting slot before any is decided,
// so a public slot never pins a piece a private slot waiting beside it holds.
// A node with no public Kubo has no use for the class and reads nothing.
func (r *Runner) noteClasses(ctx context.Context) error {
	if r.pins == nil {
		return nil
	}
	var errs []error
	for _, p := range r.pendingCopy() {
		if p.Class != 0 {
			continue
		}
		deal, err := r.chain.Deal(ctx, p.DealID)
		if err != nil {
			errs = append(errs, fmt.Errorf("read deal %d: %w", p.DealID, err))
			continue
		}
		errs = append(errs, r.notePendingClass(p.DealID, p.Slot, deal.Class))
	}
	return errors.Join(errs...)
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
	if stored {
		reason, err := r.pinPublic(ctx, slot.DealId, slot.Index, slot.PieceRoot)
		if err != nil && latest < closes-DeclineMarginBlocks {
			return err
		}
		if err != nil {
			reason = ReasonPinFailed
		}
		if reason != "" {
			return r.declineStored(ctx, slot, reason)
		}
	}
	accept, decline, err := r.store.Decide(r.signer, r.nodeID, slot.DealId, slot.Index, slot.PieceRoot)
	if err != nil {
		return err
	}
	if accept != nil {
		if err := r.chain.Submit(ctx, accept); err != nil {
			return fmt.Errorf("accept deal %d slot %d: %w", slot.DealId, slot.Index, err)
		}
		r.accepted = true
	} else {
		if err := r.chain.Submit(ctx, decline); err != nil {
			return fmt.Errorf("decline deal %d slot %d: %w", slot.DealId, slot.Index, err)
		}
	}
	return r.dropPending(slot.DealId, slot.Index)
}

// declineStored declines a slot whose piece is stored but must not be kept.
// It discards the piece first (unpinning its public CIDs) unless another
// waiting slot needs the same root, so a failed unpin is retried on the next
// step and never leaves a pin behind a declined slot.
func (r *Runner) declineStored(ctx context.Context, slot types.Slot, reason string) error {
	name, ok, err := r.store.FindByRoot(slot.PieceRoot)
	if err != nil {
		return err
	}
	if ok && !r.otherPendingRoot(slot.DealId, slot.Index, rootName(slot.PieceRoot)) {
		if err := r.store.Discard(name); err != nil {
			return err
		}
	}
	msg := &types.MsgDeclineDeal{Signer: r.signer, NodeId: r.nodeID, DealId: slot.DealId, Slot: slot.Index, Reason: reason}
	if err := r.chain.Submit(ctx, msg); err != nil {
		return fmt.Errorf("decline deal %d slot %d (%s): %w", slot.DealId, slot.Index, reason, err)
	}
	return r.dropPending(slot.DealId, slot.Index)
}

// otherPendingRoot reports whether a slot other than dealID/slot waits for root.
func (r *Runner) otherPendingRoot(dealID uint64, slot uint32, root string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.state.Pending {
		if p.Root == root && (p.DealID != dealID || p.Slot != slot) {
			return true
		}
	}
	return false
}

// answer proves every unproved challenge this node holds for epoch, in
// batches of MaxProofsPerTx. It returns how many challenges have no local
// piece. A proof tx that is dropped is rebuilt on the next step, because the
// chain still lists the challenge as unproved.
func (r *Runner) answer(ctx context.Context, epoch uint64) (int, error) {
	listed, err := r.chain.Challenges(ctx, epoch, r.nodeID)
	if err != nil {
		return 0, err
	}
	challenges, readErr := r.heldChallenges(ctx, listed)
	proofs, missing, err := r.store.AnswerChallenges(epoch, r.nodeID, challenges)
	if err != nil {
		return 0, err
	}
	for start := 0; start < len(proofs); start += MaxProofsPerTx {
		end := min(start+MaxProofsPerTx, len(proofs))
		msg := &types.MsgSubmitProofs{Signer: r.signer, NodeId: r.nodeID, Proofs: proofs[start:end]}
		if err := r.chain.Submit(ctx, msg); err != nil {
			return 0, errors.Join(readErr, fmt.Errorf("submit %d proofs for epoch %d: %w", end-start, epoch, err))
		}
	}
	return len(missing), readErr
}

// heldChallenges keeps the unproved challenges on slots the chain still
// assigns to this node. A challenge opened before the slot was evicted or
// reassigned stays listed, and the chain refuses a proof for it, which would
// fail the whole batch.
func (r *Runner) heldChallenges(ctx context.Context, listed []types.Challenge) ([]types.Challenge, error) {
	var out []types.Challenge
	var errs []error
	for _, ch := range listed {
		if ch.Proved {
			continue
		}
		slot, err := r.chain.Slot(ctx, ch.DealId, ch.Slot)
		if err != nil {
			// One failed read skips only that challenge; the rest are still proved.
			errs = append(errs, fmt.Errorf("deal %d slot %d: %w", ch.DealId, ch.Slot, err))
			continue
		}
		if holds(slot, r.nodeID) {
			out = append(out, ch)
		}
	}
	return out, errors.Join(errs...)
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
		if err := r.store.Release(a.DealID, a.Slot, r.Assigned); err != nil {
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
