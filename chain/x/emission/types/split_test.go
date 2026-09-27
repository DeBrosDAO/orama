package types_test

import (
	"testing"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	"github.com/DeBrosOfficial/network/chain/x/emission/types"
)

func TestSplitEpochMint_epoch1TotalSplitsExactly(t *testing.T) {
	// Epoch 1's maximum is 14,848 ORAMA = 14,848,000,000,000 norama. Every share is hard-coded
	// here, not re-derived from SplitEpochMint's own formula.
	total := oramaAmt(14848)
	split := types.SplitEpochMint(total)

	require.True(t, split.Storage.Equal(math.NewInt(3_712_000_000_000)), "storage (25%%)")
	require.True(t, split.Relay.Equal(math.NewInt(1_484_800_000_000)), "relay (10%%)")
	require.True(t, split.Development.Equal(math.NewInt(742_400_000_000)), "development (5%%)")
	require.True(t, split.Validator.Equal(math.NewInt(8_908_800_000_000)), "validator (60%%)")

	sum := split.Validator.Add(split.Storage).Add(split.Relay).Add(split.Development)
	require.True(t, sum.Equal(total))
}

func TestSplitEpochMint_tailEpochTotalSplitsExactly(t *testing.T) {
	// The permanent tail's maximum is 274 ORAMA = 274,000,000,000 norama.
	total := oramaAmt(274)
	split := types.SplitEpochMint(total)

	require.True(t, split.Storage.Equal(math.NewInt(68_500_000_000)), "storage (25%%)")
	require.True(t, split.Relay.Equal(math.NewInt(27_400_000_000)), "relay (10%%)")
	require.True(t, split.Development.Equal(math.NewInt(13_700_000_000)), "development (5%%)")
	require.True(t, split.Validator.Equal(math.NewInt(164_400_000_000)), "validator (60%%)")

	sum := split.Validator.Add(split.Storage).Add(split.Relay).Add(split.Development)
	require.True(t, sum.Equal(total))
}

func TestSplitEpochMint_remainderGoesToValidator(t *testing.T) {
	// 101 is not a clean multiple of 100: storage=101*25/100=25 (truncated from 25.25), relay=10
	// (from 10.1), development=5 (from 5.05); the validator share must absorb exactly the 61 left
	// over (101-25-10-5), not 101*60/100=60 (which would lose 1).
	total := math.NewInt(101)
	split := types.SplitEpochMint(total)

	require.True(t, split.Storage.Equal(math.NewInt(25)))
	require.True(t, split.Relay.Equal(math.NewInt(10)))
	require.True(t, split.Development.Equal(math.NewInt(5)))
	require.True(t, split.Validator.Equal(math.NewInt(61)))

	sum := split.Validator.Add(split.Storage).Add(split.Relay).Add(split.Development)
	require.True(t, sum.Equal(total), "split must always sum exactly to the input total")
}

func TestSplitEpochMint_zeroTotal(t *testing.T) {
	split := types.SplitEpochMint(math.ZeroInt())
	require.True(t, split.Validator.IsZero())
	require.True(t, split.Storage.IsZero())
	require.True(t, split.Relay.IsZero())
	require.True(t, split.Development.IsZero())
}

func TestSplitEpochMint_neverNegative(t *testing.T) {
	for _, epoch := range []uint64{1, 730, 731, 1461, 2921, 3650, 3651, 10000} {
		total := types.MaxMintableForEpoch(epoch)
		split := types.SplitEpochMint(total)
		require.False(t, split.Validator.IsNegative())
		require.False(t, split.Storage.IsNegative())
		require.False(t, split.Relay.IsNegative())
		require.False(t, split.Development.IsNegative())
	}
}
