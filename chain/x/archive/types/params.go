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
	return nil
}
