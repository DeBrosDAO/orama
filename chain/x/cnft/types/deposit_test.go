package types

import (
	"testing"

	"cosmossdk.io/math"

	"github.com/stretchr/testify/require"
)

func TestTreeDeposit_isDepthBufferAndCanopy(t *testing.T) {
	// 128 header + 2*(32+3*32+4) changelog + (32+3*32+4) rightmost + 2*32 canopy.
	const want = 128 + 2*132 + 132 + 64
	got, err := TreeDeposit(3, 2, 1)
	require.NoError(t, err)
	require.True(t, got.Equal(math.NewInt(want)))

	deeper, err := TreeDeposit(4, 2, 1)
	require.NoError(t, err)
	wider, err := TreeDeposit(3, 4, 1)
	require.NoError(t, err)
	tallerCanopy, err := TreeDeposit(3, 2, 2)
	require.NoError(t, err)
	require.True(t, deeper.GT(got))
	require.True(t, wider.GT(got))
	require.True(t, tallerCanopy.GT(got))
}

func TestTreeDeposit_rejectsBadShape(t *testing.T) {
	cases := [][3]uint32{{0, 1, 0}, {31, 1, 0}, {3, 0, 0}, {3, 2, 3}, {20, 2, 15}, {4, 2049, 0}}
	for _, c := range cases {
		_, err := TreeDeposit(c[0], c[1], c[2])
		require.Error(t, err, "shape %v", c)
	}
}
