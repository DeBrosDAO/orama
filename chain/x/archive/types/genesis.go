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

// Validate checks one range record. Archived is true only at quorum.
func (r RangeRecord) Validate() error {
	if err := ValidateHeights(r.StartHeight, r.EndHeight); err != nil {
		return err
	}
	if err := ValidateBundleCID(r.BundleCid); err != nil {
		return err
	}
	if err := ValidateHash("bundle_hash", r.BundleHash); err != nil {
		return err
	}
	if err := ValidateHash("merkle_root", r.MerkleRoot); err != nil {
		return err
	}
	if len(r.Archivers) == 0 {
		return fmt.Errorf("range %d-%d has no archivers", r.StartHeight, r.EndHeight)
	}
	if len(r.Archivers) > MaxArchiversPerRange {
		return fmt.Errorf("range %d-%d has %d archivers, max is %d", r.StartHeight, r.EndHeight, len(r.Archivers), MaxArchiversPerRange)
	}
	seenArchivers := make(map[string]struct{}, len(r.Archivers))
	for _, archiver := range r.Archivers {
		addr, err := sdk.AccAddressFromBech32(archiver)
		if err != nil {
			return fmt.Errorf("range %d-%d archiver %q: %w", r.StartHeight, r.EndHeight, archiver, err)
		}
		if addr.String() != archiver {
			return fmt.Errorf("range %d-%d archiver %s is not canonical bech32", r.StartHeight, r.EndHeight, archiver)
		}
		if _, ok := seenArchivers[string(addr)]; ok {
			return fmt.Errorf("duplicate archiver %s on range %d-%d", archiver, r.StartHeight, r.EndHeight)
		}
		seenArchivers[string(addr)] = struct{}{}
	}
	if len(r.Operators) != len(r.Archivers) {
		return fmt.Errorf("range %d-%d has %d archivers but %d operators", r.StartHeight, r.EndHeight, len(r.Archivers), len(r.Operators))
	}
	seenOperators := make(map[string]struct{}, len(r.Operators))
	for _, op := range r.Operators {
		addr, err := sdk.AccAddressFromBech32(op)
		if err != nil || addr.String() != op {
			return fmt.Errorf("range %d-%d operator %q is not a canonical account address", r.StartHeight, r.EndHeight, op)
		}
		if _, ok := seenOperators[op]; ok {
			return fmt.Errorf("range %d-%d repeats operator %s", r.StartHeight, r.EndHeight, op)
		}
		seenOperators[op] = struct{}{}
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
