package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPickRangeBlocks(t *testing.T) {
	got, err := pickRangeBlocks(0, 1000)
	require.NoError(t, err)
	require.Equal(t, int64(1000), got, "the default reads the chain's width")

	got, err = pickRangeBlocks(1000, 1000)
	require.NoError(t, err)
	require.Equal(t, int64(1000), got, "a flag equal to the chain's width is accepted")

	_, err = pickRangeBlocks(500, 1000)
	require.ErrorContains(t, err, "differs from the chain's range_blocks 1000")

	_, err = pickRangeBlocks(-1, 1000)
	require.Error(t, err)

	_, err = pickRangeBlocks(0, 0)
	require.Error(t, err, "a chain that reports no width is refused, not defaulted")
}
