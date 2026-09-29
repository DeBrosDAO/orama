package keeper

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strconv"

	"cosmossdk.io/collections"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/archive/types"
)

// Attest pins a range on its first attestation and counts later archivers only
// when they repeat that same root. A different root is refused and is not
// stored, so it does not count toward the pinned root.
//
// The signer must be the hot key of msg.NodeId, an x/nodes node with an
// active ARCHIVER role bond. Each operator counts once: a repeat by the same
// key or by another node of the same operator is accepted and changes
// nothing, so an operator's second archiver or a rotated key does not stall.
func (k Keeper) Attest(ctx sdk.Context, msg *types.MsgAttest) (bool, uint32, error) {
	if msg == nil {
		return false, 0, fmt.Errorf("nil MsgAttest")
	}
	archiver, err := types.ValidateAttestation(msg.Archiver, msg.NodeId, msg.StartHeight, msg.EndHeight, msg.BundleCid, msg.BundleHash, msg.MerkleRoot)
	if err != nil {
		return false, 0, err
	}
	if err := requireFinalized(ctx, msg.EndHeight); err != nil {
		return false, 0, err
	}
	key := collections.Join(msg.StartHeight, msg.EndHeight)
	rec, err := k.Ranges.Get(ctx, key)
	fresh := errors.Is(err, collections.ErrNotFound)
	if err != nil && !fresh {
		return false, 0, fmt.Errorf("failed to get range %d-%d: %w", msg.StartHeight, msg.EndHeight, err)
	}
	canonical := archiver.String()
	if !fresh {
		if err := matchPinned(rec, msg); err != nil {
			return false, 0, err
		}
		if slices.Contains(rec.Archivers, canonical) {
			return rec.Archived, uint32(len(rec.Archivers)), nil
		}
	}
	operator, err := k.nodes.ArchiverOperator(ctx, msg.NodeId, canonical)
	if err != nil {
		return false, 0, fmt.Errorf("archiver %s: %w", canonical, err)
	}
	if fresh {
		if err := k.rejectOverlap(ctx, msg.StartHeight, msg.EndHeight); err != nil {
			return false, 0, err
		}
		rec = types.RangeRecord{
			StartHeight: msg.StartHeight, EndHeight: msg.EndHeight, BundleCid: msg.BundleCid,
			BundleHash: append([]byte(nil), msg.BundleHash...), MerkleRoot: append([]byte(nil), msg.MerkleRoot...),
		}
	} else if slices.Contains(rec.Operators, operator) {
		return rec.Archived, uint32(len(rec.Archivers)), nil
	}
	if len(rec.Archivers) >= types.MaxArchiversPerRange {
		return false, 0, fmt.Errorf("range %d-%d already has %d archivers", msg.StartHeight, msg.EndHeight, len(rec.Archivers))
	}
	rec.Archivers = append(append([]string(nil), rec.Archivers...), canonical)
	rec.Operators = append(append([]string(nil), rec.Operators...), operator)
	stored, justArchived, err := k.storeRange(ctx, rec)
	if err != nil {
		return false, 0, err
	}
	emitAttest(ctx, stored, canonical, justArchived)
	return stored.Archived, uint32(len(stored.Archivers)), nil
}

// matchPinned refuses an attestation whose root or bundle differs from the
// range's first attestation.
func matchPinned(rec types.RangeRecord, msg *types.MsgAttest) error {
	if !bytes.Equal(rec.MerkleRoot, msg.MerkleRoot) {
		return fmt.Errorf("%w: range %d-%d", types.ErrWrongRoot, msg.StartHeight, msg.EndHeight)
	}
	if rec.BundleCid != msg.BundleCid || !bytes.Equal(rec.BundleHash, msg.BundleHash) {
		return fmt.Errorf("%w: range %d-%d", types.ErrWrongBundle, msg.StartHeight, msg.EndHeight)
	}
	return nil
}

// AttachReplicas records deal ids for an attested range. The signer must be
// the hot key of an active ARCHIVER node. Each id must be an active x/storage
// ARCHIVE deal that backs no other range; deal creation and payment stay in
// x/storage.
func (k Keeper) AttachReplicas(ctx sdk.Context, msg *types.MsgAttachReplicas) (bool, uint32, error) {
	if msg == nil {
		return false, 0, fmt.Errorf("nil MsgAttachReplicas")
	}
	archiver, err := types.ValidateAttach(msg.Archiver, msg.NodeId, msg.StartHeight, msg.EndHeight, msg.DealIds)
	if err != nil {
		return false, 0, err
	}
	if err := requireFinalized(ctx, msg.EndHeight); err != nil {
		return false, 0, err
	}
	if _, err := k.nodes.ArchiverOperator(ctx, msg.NodeId, archiver.String()); err != nil {
		return false, 0, fmt.Errorf("archiver %s: %w", archiver, err)
	}
	rec, err := k.GetRange(ctx, msg.StartHeight, msg.EndHeight)
	if err != nil {
		return false, 0, err
	}
	if err := k.requireArchiveDeals(ctx, rec.StartHeight, msg.DealIds); err != nil {
		return false, 0, err
	}
	merged, changed, err := mergeDealIDs(rec.DealIds, msg.DealIds)
	if err != nil {
		return false, 0, err
	}
	if !changed {
		return rec.Archived, uint32(len(rec.DealIds)), nil
	}
	if err := k.indexDeals(ctx, rec.StartHeight, msg.DealIds); err != nil {
		return false, 0, err
	}
	rec.DealIds = merged
	stored, justArchived, err := k.storeRange(ctx, rec)
	if err != nil {
		return false, 0, err
	}
	emitAttach(ctx, stored, archiver.String(), justArchived)
	return stored.Archived, uint32(len(stored.DealIds)), nil
}

// requireArchiveDeals checks that every id is an active x/storage ARCHIVE
// deal that backs no other range. The deal's content is not checked against
// the bundle: x/storage has no way yet to create an ARCHIVE deal for a given
// bundle, so the three attesting operators are what vouch for the bundle.
func (k Keeper) requireArchiveDeals(ctx sdk.Context, start int64, ids []string) error {
	for _, id := range ids {
		dealID, err := strconv.ParseUint(id, 10, 64)
		if err != nil || dealID == 0 {
			return fmt.Errorf("%w: %q is not a deal id", types.ErrNotArchiveDeal, id)
		}
		ok, err := k.storage.ArchiveDealActive(ctx, dealID)
		if err != nil {
			return fmt.Errorf("read deal %d: %w", dealID, err)
		}
		if !ok {
			return fmt.Errorf("%w: deal %d", types.ErrNotArchiveDeal, dealID)
		}
		owner, err := k.AttachedDeals.Get(ctx, dealID)
		if err == nil && owner != start {
			return fmt.Errorf("%w: deal %d backs the range starting at %d", types.ErrDealAttached, dealID, owner)
		}
		if err != nil && !errors.Is(err, collections.ErrNotFound) {
			return fmt.Errorf("read attached deal %d: %w", dealID, err)
		}
	}
	return nil
}

// indexDeals records that ids back the range starting at start.
func (k Keeper) indexDeals(ctx sdk.Context, start int64, ids []string) error {
	for _, id := range ids {
		dealID, err := strconv.ParseUint(id, 10, 64)
		if err != nil {
			return fmt.Errorf("deal id %q: %w", id, err)
		}
		if err := k.AttachedDeals.Set(ctx, dealID, start); err != nil {
			return fmt.Errorf("index deal %d: %w", dealID, err)
		}
	}
	return nil
}

// liveDeals counts the recorded deal ids that are still active ARCHIVE deals.
func (k Keeper) liveDeals(ctx sdk.Context, ids []string) (int, error) {
	live := 0
	for _, id := range ids {
		dealID, err := strconv.ParseUint(id, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("deal id %q: %w", id, err)
		}
		ok, err := k.storage.ArchiveDealActive(ctx, dealID)
		if err != nil {
			return 0, fmt.Errorf("read deal %d: %w", dealID, err)
		}
		if ok {
			live++
		}
	}
	return live, nil
}

func requireFinalized(ctx sdk.Context, end int64) error {
	tip := ctx.BlockHeight()
	if end >= tip {
		return fmt.Errorf("%w: range ends at %d, current height is %d", types.ErrNotFinalized, end, tip)
	}
	return nil
}

func (k Keeper) rejectOverlap(ctx sdk.Context, start, end int64) error {
	var overlap error
	err := k.Ranges.Walk(ctx, nil, func(_ collections.Pair[int64, int64], rec types.RangeRecord) (bool, error) {
		if start <= rec.EndHeight && rec.StartHeight <= end {
			overlap = fmt.Errorf("%w: %d-%d overlaps %d-%d", types.ErrOverlap, start, end, rec.StartHeight, rec.EndHeight)
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("failed to walk ranges: %w", err)
	}
	return overlap
}

func mergeDealIDs(existing, add []string) ([]string, bool, error) {
	seen := make(map[string]struct{}, len(existing)+len(add))
	out := append([]string(nil), existing...)
	for _, id := range existing {
		seen[id] = struct{}{}
	}
	changed := false
	for _, id := range add {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
		changed = true
	}
	if len(out) > types.MaxDealIDsPerRange {
		return nil, false, fmt.Errorf("range would have %d deal ids, max is %d", len(out), types.MaxDealIDsPerRange)
	}
	return out, changed, nil
}

// storeRange marks the range archived when both thresholds are met and, on
// that transition, extends the contiguous archived prefix.
func (k Keeper) storeRange(ctx sdk.Context, rec types.RangeRecord) (types.RangeRecord, bool, error) {
	was := rec.Archived
	if !was && types.QuorumMet(len(rec.Archivers), len(rec.DealIds)) {
		// A deal attached earlier may have ended since; only live ones count
		// at the moment the range becomes archived. Archived is permanent.
		live, err := k.liveDeals(ctx, rec.DealIds)
		if err != nil {
			return types.RangeRecord{}, false, err
		}
		rec.Archived = types.QuorumMet(len(rec.Archivers), live)
	}
	key := collections.Join(rec.StartHeight, rec.EndHeight)
	if err := k.Ranges.Set(ctx, key, rec); err != nil {
		return types.RangeRecord{}, false, fmt.Errorf("failed to store range %d-%d: %w", rec.StartHeight, rec.EndHeight, err)
	}
	justArchived := rec.Archived && !was
	if justArchived {
		if err := k.extendLastArchived(ctx); err != nil {
			return types.RangeRecord{}, false, err
		}
	}
	return rec, justArchived, nil
}

func (k Keeper) extendLastArchived(ctx sdk.Context) error {
	next, err := k.recomputeLastArchived(ctx)
	if err != nil {
		return err
	}
	prev, err := k.LastArchivedHeight.Get(ctx)
	if err != nil {
		return fmt.Errorf("failed to get last archived height: %w", err)
	}
	if next < prev {
		return fmt.Errorf("last archived height decreased from %d to %d", prev, next)
	}
	if next == prev {
		return nil
	}
	if err := k.LastArchivedHeight.Set(ctx, next); err != nil {
		return fmt.Errorf("failed to set last archived height: %w", err)
	}
	return nil
}

func (k Keeper) recomputeLastArchived(ctx sdk.Context) (int64, error) {
	var ranges []types.RangeRecord
	err := k.Ranges.Walk(ctx, nil, func(_ collections.Pair[int64, int64], rec types.RangeRecord) (bool, error) {
		ranges = append(ranges, rec)
		return false, nil
	})
	if err != nil {
		return 0, fmt.Errorf("failed to walk ranges: %w", err)
	}
	return types.ContiguousArchivedHeight(ranges), nil
}

func emitAttest(ctx sdk.Context, rec types.RangeRecord, archiver string, justArchived bool) {
	ctx.EventManager().EmitEvent(rangeEvent(types.EventTypeAttest, rec, archiver))
	if justArchived {
		ctx.EventManager().EmitEvent(rangeEvent(types.EventTypeArchived, rec, archiver))
	}
}

func emitAttach(ctx sdk.Context, rec types.RangeRecord, archiver string, justArchived bool) {
	ctx.EventManager().EmitEvent(rangeEvent(types.EventTypeAttachReplicas, rec, archiver))
	if justArchived {
		ctx.EventManager().EmitEvent(rangeEvent(types.EventTypeArchived, rec, archiver))
	}
}

func rangeEvent(eventType string, rec types.RangeRecord, archiver string) sdk.Event {
	return sdk.NewEvent(
		eventType,
		sdk.NewAttribute(types.AttributeKeyArchiver, archiver),
		sdk.NewAttribute(types.AttributeKeyStartHeight, strconv.FormatInt(rec.StartHeight, 10)),
		sdk.NewAttribute(types.AttributeKeyEndHeight, strconv.FormatInt(rec.EndHeight, 10)),
		sdk.NewAttribute(types.AttributeKeyBundleCID, rec.BundleCid),
		sdk.NewAttribute(types.AttributeKeyArchived, strconv.FormatBool(rec.Archived)),
	)
}
