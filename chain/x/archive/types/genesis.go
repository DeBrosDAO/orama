package types

import (
	"fmt"
	"sort"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// DefaultGenesisState returns an empty registry and the default 14-day window.
func DefaultGenesisState() *GenesisState {
	return &GenesisState{
		Params:             DefaultParams(),
		Ranges:             []RangeRecord{},
		LastArchivedHeight: 0,
	}
}

// ContiguousArchivedHeight is the highest H such that every block in 1..H
// belongs to an archived range. A gap or an unarchived range that covers the
// next height stops the prefix. Ranges should not overlap; Validate rejects
// that separately.
func ContiguousArchivedHeight(ranges []RangeRecord) int64 {
	if len(ranges) == 0 {
		return 0
	}
	ordered := append([]RangeRecord(nil), ranges...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].StartHeight != ordered[j].StartHeight {
			return ordered[i].StartHeight < ordered[j].StartHeight
		}
		return ordered[i].EndHeight < ordered[j].EndHeight
	})

	next := int64(1)
	last := int64(0)
	for _, r := range ordered {
		if r.EndHeight < next {
			continue
		}
		if r.StartHeight > next {
			break
		}
		if !r.Archived {
			break
		}
		last = r.EndHeight
		next = r.EndHeight + 1
	}
	return last
}

// Validate checks one range record. A decided range carries its winning tuple, at least
// MinArchiverAttestations operators and no candidates; Archived is true only at quorum. A range that is
// not decided carries no winning tuple, no deals and no operators, and one to
// MaxCandidatesLimit candidates each short of the operator quorum, no operator in two of them.
func (r RangeRecord) Validate() error {
	if err := ValidateHeights(r.StartHeight, r.EndHeight); err != nil {
		return err
	}
	if !r.Decided {
		return r.validateUndecided()
	}
	if len(r.Candidates) != 0 {
		return fmt.Errorf("range %d-%d is decided but keeps %d candidates", r.StartHeight, r.EndHeight, len(r.Candidates))
	}
	if err := r.Winner().Validate(); err != nil {
		return fmt.Errorf("range %d-%d: %w", r.StartHeight, r.EndHeight, err)
	}
	if err := validateAttesters(fmt.Sprintf("range %d-%d", r.StartHeight, r.EndHeight), r.Archivers, r.Operators, MinArchiverAttestations, MaxArchiversPerRange); err != nil {
		return err
	}
	if len(r.DealIds) > MaxDealIDsPerRange {
		return fmt.Errorf("range %d-%d has %d deal ids, max is %d", r.StartHeight, r.EndHeight, len(r.DealIds), MaxDealIDsPerRange)
	}
	seenDeals := make(map[string]struct{}, len(r.DealIds))
	for _, id := range r.DealIds {
		if err := ValidateDealID(id); err != nil {
			return fmt.Errorf("range %d-%d: %w", r.StartHeight, r.EndHeight, err)
		}
		if _, ok := seenDeals[id]; ok {
			return fmt.Errorf("duplicate deal id %q on range %d-%d", id, r.StartHeight, r.EndHeight)
		}
		seenDeals[id] = struct{}{}
	}
	met := QuorumMet(len(r.Archivers), len(r.DealIds))
	if r.Archived && !met {
		return fmt.Errorf("range %d-%d is archived without 3 archivers and 3 deal ids", r.StartHeight, r.EndHeight)
	}
	if !r.Archived && met {
		return fmt.Errorf("range %d-%d meets quorum but is not marked archived", r.StartHeight, r.EndHeight)
	}
	return nil
}

func (r RangeRecord) validateUndecided() error {
	where := fmt.Sprintf("range %d-%d", r.StartHeight, r.EndHeight)
	if !r.Winner().isZero() || len(r.Archivers) != 0 || len(r.Operators) != 0 || len(r.DealIds) != 0 || r.Archived {
		return fmt.Errorf("%s is not decided but carries a winning tuple, attesters, deals or the archived mark", where)
	}
	if len(r.Candidates) == 0 || len(r.Candidates) > int(MaxCandidatesLimit) {
		return fmt.Errorf("%s is not decided and has %d candidates, want 1 to %d", where, len(r.Candidates), MaxCandidatesLimit)
	}
	seenOperators := map[string]struct{}{}
	for i, c := range r.Candidates {
		if err := c.Tuple().Validate(); err != nil {
			return fmt.Errorf("%s candidate %d: %w", where, i, err)
		}
		for j := 0; j < i; j++ {
			if c.Tuple().Equal(r.Candidates[j].Tuple()) {
				return fmt.Errorf("%s repeats candidate tuple %d", where, i)
			}
		}
		if err := validateAttesters(fmt.Sprintf("%s candidate %d", where, i), c.Archivers, c.Operators, 1, MinArchiverAttestations-1); err != nil {
			return err
		}
		for _, op := range c.Operators {
			if _, ok := seenOperators[op]; ok {
				return fmt.Errorf("%s: operator %s attested two candidate tuples", where, op)
			}
			seenOperators[op] = struct{}{}
		}
	}
	return nil
}

// validateAttesters checks a list of archivers and the operators paired with them: between min and
// max entries, canonical bech32 addresses, no repeats, one operator per archiver.
func validateAttesters(where string, archivers, operators []string, min, max int) error {
	if len(archivers) < min || len(archivers) > max {
		return fmt.Errorf("%s has %d archivers, want %d to %d", where, len(archivers), min, max)
	}
	seenArchivers := make(map[string]struct{}, len(archivers))
	for _, archiver := range archivers {
		addr, err := sdk.AccAddressFromBech32(archiver)
		if err != nil {
			return fmt.Errorf("%s archiver %q: %w", where, archiver, err)
		}
		if addr.String() != archiver {
			return fmt.Errorf("%s archiver %s is not canonical bech32", where, archiver)
		}
		if _, ok := seenArchivers[string(addr)]; ok {
			return fmt.Errorf("duplicate archiver %s on %s", archiver, where)
		}
		seenArchivers[string(addr)] = struct{}{}
	}
	if len(operators) != len(archivers) {
		return fmt.Errorf("%s has %d archivers but %d operators", where, len(archivers), len(operators))
	}
	seenOperators := make(map[string]struct{}, len(operators))
	for _, op := range operators {
		addr, err := sdk.AccAddressFromBech32(op)
		if err != nil || addr.String() != op {
			return fmt.Errorf("%s operator %q is not a canonical account address", where, op)
		}
		if _, ok := seenOperators[op]; ok {
			return fmt.Errorf("%s repeats operator %s", where, op)
		}
		seenOperators[op] = struct{}{}
	}
	return nil
}

// Validate checks genesis state. last_archived_height must be the contiguous
// archived prefix, not the highest archived end in the list.
func (gs GenesisState) Validate() error {
	if err := gs.Params.Validate(); err != nil {
		return err
	}
	ordered := append([]RangeRecord(nil), gs.Ranges...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].StartHeight != ordered[j].StartHeight {
			return ordered[i].StartHeight < ordered[j].StartHeight
		}
		return ordered[i].EndHeight < ordered[j].EndHeight
	})
	dealOwner := map[string]int64{}
	for i, r := range ordered {
		if err := r.Validate(); err != nil {
			return err
		}
		if r.PieceBytes > gs.Params.MaxPieceBytes {
			return fmt.Errorf("%w: range %d-%d commits %d bytes, max_piece_bytes is %d", ErrPieceTooLarge, r.StartHeight, r.EndHeight, r.PieceBytes, gs.Params.MaxPieceBytes)
		}
		if len(r.Candidates) > int(gs.Params.MaxCandidatesPerRange) {
			return fmt.Errorf("range %d-%d has %d candidates, max_candidates_per_range is %d", r.StartHeight, r.EndHeight, len(r.Candidates), gs.Params.MaxCandidatesPerRange)
		}
		for _, c := range r.Candidates {
			if c.PieceBytes > gs.Params.MaxPieceBytes {
				return fmt.Errorf("%w: range %d-%d candidate commits %d bytes, max_piece_bytes is %d", ErrPieceTooLarge, r.StartHeight, r.EndHeight, c.PieceBytes, gs.Params.MaxPieceBytes)
			}
		}
		for _, id := range r.DealIds {
			if start, ok := dealOwner[id]; ok {
				return fmt.Errorf("deal %s backs both the range starting at %d and the one at %d", id, start, r.StartHeight)
			}
			dealOwner[id] = r.StartHeight
		}
		if i == 0 {
			continue
		}
		prev := ordered[i-1]
		if prev.StartHeight == r.StartHeight && prev.EndHeight == r.EndHeight {
			return fmt.Errorf("duplicate range %d-%d", r.StartHeight, r.EndHeight)
		}
		if prev.EndHeight >= r.StartHeight {
			return fmt.Errorf("%w: %d-%d overlaps %d-%d", ErrOverlap, prev.StartHeight, prev.EndHeight, r.StartHeight, r.EndHeight)
		}
	}
	got := ContiguousArchivedHeight(gs.Ranges)
	if gs.LastArchivedHeight != got {
		return fmt.Errorf("last_archived_height %d does not match the contiguous archived prefix %d", gs.LastArchivedHeight, got)
	}
	return nil
}
