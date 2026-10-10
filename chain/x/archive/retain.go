package archive

import "github.com/DeBrosOfficial/network/chain/x/archive/types"

// RetainHeight is min(tip-blocksIn14Days, lastArchivedHeight).
// It is never above lastArchivedHeight. CometBFT prunes blocks strictly
// below this height; a stalled archive keeps the retain height on the last
// archived block even if the tip is a year ahead.
func RetainHeight(tip, blocksIn14Days, lastArchivedHeight int64) int64 {
	return types.RetainHeight(tip, blocksIn14Days, lastArchivedHeight)
}

// PruneAllowed reports whether the block at height may be deleted given the
// same inputs as RetainHeight. Pruning at or past that retain height is refused.
func PruneAllowed(height, tip, blocksIn14Days, lastArchivedHeight int64) bool {
	return types.PruneAllowed(height, tip, blocksIn14Days, lastArchivedHeight)
}
