package types

// RetainHeight is min(tip-blocksIn14Days, lastArchivedHeight).
//
// The result is never above lastArchivedHeight. If archiving stalls, the
// 14-day term runs ahead of the archived prefix and the retain height stays
// at lastArchivedHeight, including when the tip is a year ahead.
//
// CometBFT deletes blocks strictly below the height returned here. A
// non-positive result means prune nothing. Callers must not pass a
// non-positive value through as a prune target.
func RetainHeight(tip, blocksIn14Days, lastArchivedHeight int64) int64 {
	windowFloor := tip - blocksIn14Days
	if lastArchivedHeight < windowFloor {
		return lastArchivedHeight
	}
	return windowFloor
}

// PruneAllowed reports whether the block at height may be deleted.
//
// Deletion at or above RetainHeight is refused. Because that retain height
// is never above lastArchivedHeight, a node also cannot prune past the last
// archived height. Blocks inside the 14-day window stay even when they are
// already archived.
func PruneAllowed(height, tip, blocksIn14Days, lastArchivedHeight int64) bool {
	if height <= 0 {
		return false
	}
	retain := RetainHeight(tip, blocksIn14Days, lastArchivedHeight)
	if retain <= 0 {
		return false
	}
	return height < retain && height <= lastArchivedHeight
}
