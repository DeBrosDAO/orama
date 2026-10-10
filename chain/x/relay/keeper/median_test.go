package keeper

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"
)

func TestMedianInt_evenCountNearTheTypeLimitDoesNotOverflow(t *testing.T) {
	// math.Int holds at most 256 bits, so two of these sum past the type.
	max := math.NewIntFromBigInt(new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1)))
	require.NotPanics(t, func() {
		require.True(t, medianInt([]math.Int{max, max}).Equal(max))
	})
}

func TestMedianInt_floorsTheAverageOfTheMiddlePair(t *testing.T) {
	require.True(t, medianInt([]math.Int{math.NewInt(1), math.NewInt(4)}).Equal(math.NewInt(2)))
	require.True(t, medianInt([]math.Int{math.NewInt(5), math.NewInt(1), math.NewInt(3)}).Equal(math.NewInt(3)))
	require.True(t, medianInt([]math.Int{math.NewInt(7)}).Equal(math.NewInt(7)))
}
