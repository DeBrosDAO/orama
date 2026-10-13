package indexer

import (
	"context"
	"fmt"
)

// ResolveStart is the height a follower is bound to. A start height above zero is the operator's
// and is returned as it is (an index that was started from another height, or a node that has
// pruned it, refuses it later with a message that says so). Zero means "wherever this index
// stands": the start height an index already has, or, for a new index, the earliest block the
// node still serves, which is the first block it can index without a gap. A node that has pruned
// the blocks below the chain's first (an archived range, a state-synced node) would otherwise
// refuse a new index at height 1 every time the indexer starts.
func ResolveStart(ctx context.Context, chain TipSource, store *Store, start int64) (int64, error) {
	if start < 0 {
		return 0, fmt.Errorf("start height must not be negative, got %d", start)
	}
	if start > 0 {
		return start, nil
	}
	st, err := store.Status()
	if err != nil {
		return 0, err
	}
	if st.StartHeight > 0 {
		return st.StartHeight, nil
	}
	earliest, _, err := chain.HeightRange(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to read the node's earliest block to start the index from: %w", err)
	}
	return max(earliest, 1), nil
}
