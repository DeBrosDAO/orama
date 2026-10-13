package indexer

import cmttypes "github.com/cometbft/cometbft/types"

// commitTally counts the votes in a commit that are for the block, and all its entries; the first
// block has no commit.
func commitTally(c *cmttypes.Commit) (signed, size int) {
	if c == nil {
		return 0, 0
	}
	for _, sig := range c.Signatures {
		if sig.BlockIDFlag == cmttypes.BlockIDFlagCommit {
			signed++
		}
	}
	return signed, len(c.Signatures)
}
