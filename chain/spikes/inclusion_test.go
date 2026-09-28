package spikes_test

import (
	"testing"

	cmttypes "github.com/cometbft/cometbft/types"
	"github.com/stretchr/testify/require"
)

// listMaxBytes is C13's proposed per-validator cap. It is an application
// limit. CometBFT v0.39.4's own cap is MaxVoteExtensionSize.
const listMaxBytes = 32 * 1024

func TestVoteExtensionBudget(t *testing.T) {
	blockMax := cmttypes.DefaultBlockParams().MaxBytes
	require.Equal(t, int64(22_020_096), blockMax)
	require.Equal(t, 1024*1024, cmttypes.MaxVoteExtensionSize)
	require.Equal(t, 100*1024*1024, cmttypes.MaxBlockSizeBytes)
	require.Less(t, listMaxBytes, cmttypes.MaxVoteExtensionSize)

	counts := []int{30, 60, 100, 150}
	for _, n := range counts {
		raw := n * listMaxBytes
		require.Less(t, int64(raw), blockMax, "disjoint %d-validator lists still fit the default block", n)
		cometCap := n * cmttypes.MaxVoteExtensionSize
		require.Greater(t, int64(cometCap), blockMax, "CometBFT's 1MiB extension cap does not fit the default block at %d validators", n)
		t.Logf("validators=%d list_max_bytes=%d disjoint_extension_bytes=%d share_of_default_block_max=%.4f comet_1MiB_cap_bytes=%d exceeds_absolute_block_max=%t",
			n, listMaxBytes, raw, float64(raw)/float64(blockMax), cometCap, cometCap > cmttypes.MaxBlockSizeBytes)
	}
}
