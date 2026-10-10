package types

import "fmt"

// DefaultParams returns x/archive's genesis parameters: a 14-day window at a
// 6-second block interval. A chain whose blocks are faster must raise
// RetentionWindowBlocks in genesis or the window is shorter than 14 days.
func DefaultParams() Params {
	return Params{
		RetentionWindowBlocks: DefaultBlocksIn14Days,
		MaxPieceBytes:         DefaultMaxPieceBytes,
		MaxCandidatesPerRange: DefaultMaxCandidatesPerRange,
		RangeBlocks:           DefaultRangeBlocks,
	}
}

// Validate checks Params. There is no authority that can change them later.
func (p Params) Validate() error {
	if p.RetentionWindowBlocks < MinBlocksIn14Days || p.RetentionWindowBlocks > MaxBlocksIn14Days {
		return fmt.Errorf(
			"retention_window_blocks must be in [%d, %d], got %d",
			MinBlocksIn14Days, MaxBlocksIn14Days, p.RetentionWindowBlocks,
		)
	}
	if p.MaxPieceBytes == 0 || p.MaxPieceBytes > MaxPieceBytesLimit {
		return fmt.Errorf("max_piece_bytes must be in [1, %d], got %d", MaxPieceBytesLimit, p.MaxPieceBytes)
	}
	if p.MaxCandidatesPerRange == 0 || p.MaxCandidatesPerRange > MaxCandidatesLimit {
		return fmt.Errorf("max_candidates_per_range must be in [1, %d], got %d", MaxCandidatesLimit, p.MaxCandidatesPerRange)
	}
	if p.RangeBlocks < 1 || p.RangeBlocks > MaxRangeBlocksLimit {
		return fmt.Errorf("range_blocks must be in [1, %d], got %d", MaxRangeBlocksLimit, p.RangeBlocks)
	}
	return nil
}

// CheckCanonicalRange refuses a range that is not one of the fixed ranges of width blocks: it must
// start at k*width+1 and end at start+width-1. Canonical ranges never overlap, so one operator
// cannot hold an arbitrary span of heights or a slice of another range.
func CheckCanonicalRange(start, end, width int64) error {
	if err := ValidateHeights(start, end); err != nil {
		return err
	}
	if (start-1)%width != 0 || end != start+width-1 {
		return fmt.Errorf("%w: %d-%d, ranges are %d blocks and start at a multiple of %d plus 1", ErrNotCanonicalRange, start, end, width, width)
	}
	return nil
}
