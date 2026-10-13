package keeper

import (
	"errors"
	"fmt"
	"slices"
	"strconv"

	"cosmossdk.io/collections"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/archive/types"
)

// Attest records one archiver's attestation of a range's tuple: the bundle CID, content hash,
// block-hash Merkle root and piece commitment. Attestations are tallied per tuple, and distinct
// operators are counted within a tuple, so a first attestation cannot fix what the range is:
// archivers that agree on a different tuple are counted toward that tuple, and conflicting tuples
// coexist (at most Params.MaxCandidatesPerRange of them) until one is attested by
// MinArchiverAttestations operators. That tuple wins: the range is decided, the winner's
// fields become the range's, and the other candidates are dropped. A decided range accepts only
// further attestations of the winning tuple.
//
// The range must be canonical (Params.RangeBlocks): it starts at k*range_blocks+1 and is
// range_blocks long, so ranges never overlap and one operator cannot hold an arbitrary span. A
// candidate none of whose attesting nodes still has an active ARCHIVER role is freed when the next
// attestation of the range arrives (the nodes are read at that moment, not by a hook), so
// departed operators cannot hold the candidate slots of an undecided range.
//
// The signer must be the hot key of msg.NodeId, an x/nodes node with an active ARCHIVER role bond.
// Each operator counts once: a repeat by the same key or by another node of the same operator for
// the same tuple is accepted and changes nothing, so an operator's second archiver or a rotated key
// does not stall. An operator that attested one tuple of a range cannot attest another for it.
func (k Keeper) Attest(ctx sdk.Context, msg *types.MsgAttest) (bool, uint32, error) {
	if msg == nil {
		return false, 0, fmt.Errorf("nil MsgAttest")
	}
	archiver, err := types.ValidateAttestation(msg.Archiver, msg.NodeId, msg.StartHeight, msg.EndHeight, msg.BundleCid, msg.BundleHash, msg.MerkleRoot, msg.PieceOf())
	if err != nil {
		return false, 0, err
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return false, 0, fmt.Errorf("failed to get archive params: %w", err)
	}
	if err := types.CheckCanonicalRange(msg.StartHeight, msg.EndHeight, params.RangeBlocks); err != nil {
		return false, 0, err
	}
	if msg.PieceBytes > params.MaxPieceBytes {
		return false, 0, fmt.Errorf("%w: %d bytes, max is %d", types.ErrPieceTooLarge, msg.PieceBytes, params.MaxPieceBytes)
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
	tuple := msg.Tuple()
	if !fresh {
		// A key that already attested is idempotent even after its node lost the role.
		if done, archived, attesters, err := attestedBy(rec, tuple, canonical); done || err != nil {
			return archived, attesters, err
		}
	}
	operator, err := k.nodes.ArchiverOperator(ctx, msg.NodeId, canonical)
	if err != nil {
		return false, 0, fmt.Errorf("archiver %s: %w", canonical, err)
	}
	if fresh {
		rec = types.RangeRecord{StartHeight: msg.StartHeight, EndHeight: msg.EndHeight}
	}
	if rec.Decided {
		return k.attestDecided(ctx, rec, tuple, canonical, operator)
	}
	if rec.Candidates, err = k.liveCandidates(ctx, rec.Candidates); err != nil {
		return false, 0, err
	}
	rec, attesters, err := attestCandidate(rec, tuple, canonical, msg.NodeId, operator, params.MaxCandidatesPerRange)
	if err != nil {
		return false, 0, err
	}
	if attesters == 0 {
		// The operator already attested this tuple: nothing changes.
		return false, uint32(len(candidateOf(rec, tuple).Operators)), nil
	}
	stored, justArchived, err := k.storeRange(ctx, rec)
	if err != nil {
		return false, 0, err
	}
	emitAttest(ctx, stored, msg.BundleCid, canonical, justArchived)
	return stored.Archived, attesters, nil
}

// attestedBy reports whether the archiver key already attested rec. done is true when it attested the
// tuple (nothing changes); attesting a different tuple of the same range is an error, since an
// operator counts toward one tuple per range.
func attestedBy(rec types.RangeRecord, tuple types.Tuple, archiver string) (done, archived bool, attesters uint32, err error) {
	if rec.Decided {
		if !slices.Contains(rec.Archivers, archiver) {
			return false, false, 0, nil
		}
		if err := rec.Winner().Mismatch(tuple); err != nil {
			return false, false, 0, fmt.Errorf("%w: range %d-%d was won by a different tuple", err, rec.StartHeight, rec.EndHeight)
		}
		return true, rec.Archived, uint32(len(rec.Archivers)), nil
	}
	for _, c := range rec.Candidates {
		if !slices.Contains(c.Archivers, archiver) {
			continue
		}
		if !c.Tuple().Equal(tuple) {
			return false, false, 0, fmt.Errorf("%w: archiver %s, range %d-%d", types.ErrConflictingAttestation, archiver, rec.StartHeight, rec.EndHeight)
		}
		return true, false, uint32(len(c.Operators)), nil
	}
	return false, false, 0, nil
}

// attestDecided adds an attestation of the winning tuple to a decided range.
func (k Keeper) attestDecided(ctx sdk.Context, rec types.RangeRecord, tuple types.Tuple, canonical, operator string) (bool, uint32, error) {
	if err := rec.Winner().Mismatch(tuple); err != nil {
		return false, 0, fmt.Errorf("%w: range %d-%d was won by a different tuple", err, rec.StartHeight, rec.EndHeight)
	}
	if slices.Contains(rec.Operators, operator) {
		return rec.Archived, uint32(len(rec.Archivers)), nil
	}
	if len(rec.Archivers) >= types.MaxArchiversPerRange {
		return false, 0, fmt.Errorf("range %d-%d already has %d archivers", rec.StartHeight, rec.EndHeight, len(rec.Archivers))
	}
	rec.Archivers = append(slices.Clone(rec.Archivers), canonical)
	rec.Operators = append(slices.Clone(rec.Operators), operator)
	stored, justArchived, err := k.storeRange(ctx, rec)
	if err != nil {
		return false, 0, err
	}
	emitAttest(ctx, stored, tuple.BundleCid, canonical, justArchived)
	return stored.Archived, uint32(len(stored.Archivers)), nil
}

// attestCandidate adds the attestation to the candidate for tuple, starting one if the range
// has room, and decides the range when the candidate reaches the operator quorum. It returns the
// number of operators the tuple now has, or 0 when the operator had already attested it and the
// record is unchanged.
func attestCandidate(rec types.RangeRecord, tuple types.Tuple, archiver, nodeID, operator string, maxCandidates uint32) (types.RangeRecord, uint32, error) {
	index := -1
	for i, c := range rec.Candidates {
		if slices.Contains(c.Operators, operator) {
			if !c.Tuple().Equal(tuple) {
				return rec, 0, fmt.Errorf("%w: operator %s, range %d-%d", types.ErrConflictingAttestation, operator, rec.StartHeight, rec.EndHeight)
			}
			return rec, 0, nil
		}
		if c.Tuple().Equal(tuple) {
			index = i
		}
	}
	candidates := make([]types.Candidate, len(rec.Candidates), len(rec.Candidates)+1)
	copy(candidates, rec.Candidates)
	if index == -1 {
		if uint32(len(candidates)) >= maxCandidates {
			return rec, 0, fmt.Errorf("%w: range %d-%d has %d", types.ErrCandidatesFull, rec.StartHeight, rec.EndHeight, len(candidates))
		}
		candidates = append(candidates, types.NewCandidate(tuple))
		index = len(candidates) - 1
	}
	c := candidates[index]
	c.Archivers = append(slices.Clone(c.Archivers), archiver)
	c.Operators = append(slices.Clone(c.Operators), operator)
	c.NodeIds = append(slices.Clone(c.NodeIds), nodeID)
	candidates[index] = c
	if len(c.Operators) < types.MinArchiverAttestations {
		rec.Candidates = candidates
		return rec, uint32(len(c.Operators)), nil
	}
	rec.Decided = true
	rec.BundleCid, rec.BundleHash, rec.MerkleRoot = c.BundleCid, c.BundleHash, c.MerkleRoot
	rec.PieceRoot, rec.RealLeafCount, rec.PaddedLeafCount, rec.PieceBytes = c.PieceRoot, c.RealLeafCount, c.PaddedLeafCount, c.PieceBytes
	rec.Archivers, rec.Operators = c.Archivers, c.Operators
	rec.Candidates = nil
	return rec, uint32(len(c.Operators)), nil
}

// candidateOf returns the candidate of rec that attests tuple, or an empty one.
func candidateOf(rec types.RangeRecord, tuple types.Tuple) types.Candidate {
	for _, c := range rec.Candidates {
		if c.Tuple().Equal(tuple) {
			return c
		}
	}
	return types.Candidate{}
}

// liveCandidates returns the candidates of an undecided range that still have an attester whose
// node holds an active ARCHIVER role. A candidate every one of whose attesting nodes has lost the
// role, been jailed or retired, or no longer exists is dropped: it can never gather the operators it
// needs from its own side, and it must not keep a slot (Params.MaxCandidatesPerRange) or hold its
// operators to a tuple they can no longer stand behind.
func (k Keeper) liveCandidates(ctx sdk.Context, candidates []types.Candidate) ([]types.Candidate, error) {
	live := make([]types.Candidate, 0, len(candidates))
	for _, c := range candidates {
		alive := false
		for _, nodeID := range c.NodeIds {
			active, err := k.nodes.ArchiverActive(ctx, nodeID)
			if err != nil {
				return nil, fmt.Errorf("failed to read archiver node %s: %w", nodeID, err)
			}
			if active {
				alive = true
				break
			}
		}
		if alive {
			live = append(live, c)
		}
	}
	return live, nil
}

// AttachReplicas records deal ids for an attested range. The signer must be
// the hot key of an active ARCHIVER node whose operator attested the range. Each id must be an active x/storage
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
	operator, err := k.nodes.ArchiverOperator(ctx, msg.NodeId, archiver.String())
	if err != nil {
		return false, 0, fmt.Errorf("archiver %s: %w", archiver, err)
	}
	rec, err := k.GetRange(ctx, msg.StartHeight, msg.EndHeight)
	if err != nil {
		return false, 0, err
	}
	if !rec.Decided {
		return false, 0, fmt.Errorf("%w: %d-%d has no winning tuple yet, deals can only back one", types.ErrQuorumPending, msg.StartHeight, msg.EndHeight)
	}
	if !slices.Contains(rec.Operators, operator) {
		return false, 0, fmt.Errorf("%w: operator %s did not attest range %d-%d", types.ErrNotAttester, operator, msg.StartHeight, msg.EndHeight)
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
	if err := k.clearPending(ctx, rec.StartHeight, msg.DealIds); err != nil {
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

// clearPending forgets the pending marks of deals a range now records.
func (k Keeper) clearPending(ctx sdk.Context, start int64, ids []string) error {
	for _, id := range ids {
		dealID, err := strconv.ParseUint(id, 10, 64)
		if err != nil {
			return fmt.Errorf("deal id %q: %w", id, err)
		}
		if err := k.forgetPending(ctx, start, dealID); err != nil {
			return err
		}
	}
	return nil
}

// dropEndedDeals returns the ids that are still active ARCHIVE deals, in
// order, and removes the ended ones from the index so they back no range.
func (k Keeper) dropEndedDeals(ctx sdk.Context, ids []string) ([]string, error) {
	live := make([]string, 0, len(ids))
	for _, id := range ids {
		dealID, err := strconv.ParseUint(id, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("deal id %q: %w", id, err)
		}
		ok, err := k.storage.ArchiveDealActive(ctx, dealID)
		if err != nil {
			return nil, fmt.Errorf("read deal %d: %w", dealID, err)
		}
		if ok {
			live = append(live, id)
			continue
		}
		if err := k.AttachedDeals.Remove(ctx, dealID); err != nil {
			return nil, fmt.Errorf("release ended deal %d: %w", dealID, err)
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
		// A deal attached earlier may have ended since. Ended ones are dropped
		// (and freed in the index) so the record always satisfies
		// archived == quorum over its own ids. Archived is permanent.
		live, err := k.dropEndedDeals(ctx, rec.DealIds)
		if err != nil {
			return types.RangeRecord{}, false, err
		}
		rec.DealIds = live
		rec.Archived = types.QuorumMet(len(rec.Archivers), len(live))
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

// emitAttest reports an attestation. bundleCID is the attested tuple's: a range that is not
// decided has no bundle of its own yet.
func emitAttest(ctx sdk.Context, rec types.RangeRecord, bundleCID, archiver string, justArchived bool) {
	ctx.EventManager().EmitEvent(rangeEvent(types.EventTypeAttest, rec, bundleCID, archiver))
	if justArchived {
		ctx.EventManager().EmitEvent(rangeEvent(types.EventTypeArchived, rec, bundleCID, archiver))
	}
}

func emitAttach(ctx sdk.Context, rec types.RangeRecord, archiver string, justArchived bool) {
	ctx.EventManager().EmitEvent(rangeEvent(types.EventTypeAttachReplicas, rec, rec.BundleCid, archiver))
	if justArchived {
		ctx.EventManager().EmitEvent(rangeEvent(types.EventTypeArchived, rec, rec.BundleCid, archiver))
	}
}

func rangeEvent(eventType string, rec types.RangeRecord, bundleCID, archiver string) sdk.Event {
	return sdk.NewEvent(
		eventType,
		sdk.NewAttribute(types.AttributeKeyArchiver, archiver),
		sdk.NewAttribute(types.AttributeKeyStartHeight, strconv.FormatInt(rec.StartHeight, 10)),
		sdk.NewAttribute(types.AttributeKeyEndHeight, strconv.FormatInt(rec.EndHeight, 10)),
		sdk.NewAttribute(types.AttributeKeyBundleCID, bundleCID),
		sdk.NewAttribute(types.AttributeKeyDecided, strconv.FormatBool(rec.Decided)),
		sdk.NewAttribute(types.AttributeKeyArchived, strconv.FormatBool(rec.Archived)),
	)
}
