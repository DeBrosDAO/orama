package types_test

import (
	"testing"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	"github.com/DeBrosOfficial/network/chain/x/emission/types"
)

// oramaAmt converts a whole-ORAMA amount to norama for test assertions.
func oramaAmt(whole int64) math.Int {
	return math.NewInt(whole).MulRaw(1_000_000_000)
}

func TestMaxMintableForEpoch_boundaries(t *testing.T) {
	cases := []struct {
		name  string
		epoch uint64
		want  int64 // whole ORAMA
	}{
		{"epoch 0 mints nothing", 0, 0},
		{"epoch 1 (first bracket start)", 1, 14848},
		{"epoch 730 (first bracket end)", 730, 14848},
		{"epoch 731 (second bracket start)", 731, 7424},
		{"epoch 1460 (second bracket end)", 1460, 7424},
		{"epoch 1461 (third bracket start)", 1461, 3712},
		{"epoch 2190 (third bracket end)", 2190, 3712},
		{"epoch 2191 (fourth bracket start)", 2191, 1856},
		{"epoch 2920 (fourth bracket end)", 2920, 1856},
		{"epoch 2921 (fifth bracket start)", 2921, 928},
		{"epoch 3650 (fifth bracket end)", 3650, 928},
		{"epoch 3651 (tail start)", 3651, 274},
		{"epoch 10000 (deep into tail)", 10000, 274},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := types.MaxMintableForEpoch(tc.epoch)
			require.True(t, got.Equal(oramaAmt(tc.want)), "epoch %d: got %s, want %d ORAMA", tc.epoch, got, tc.want)
		})
	}
}

func TestCumulativeScheduleMax_epoch3650Equals21_000_640Orama(t *testing.T) {
	got := types.CumulativeScheduleMax(3650)
	want := oramaAmt(21_000_640)
	require.True(t, got.Equal(want), "got %s, want %s", got, want)
}

func TestCumulativeScheduleMax_zeroEpochIsZero(t *testing.T) {
	require.True(t, types.CumulativeScheduleMax(0).IsZero())
}

func TestCumulativeScheduleMax_monotonicallyIncreasing(t *testing.T) {
	prev := types.CumulativeScheduleMax(0)
	for _, epoch := range []uint64{1, 100, 730, 731, 1461, 2921, 3650, 3651, 4000, 10000} {
		cur := types.CumulativeScheduleMax(epoch)
		require.True(t, cur.GTE(prev), "cumulative total must never decrease: epoch %d (%s) < previous (%s)", epoch, cur, prev)
		prev = cur
	}
}

func TestCumulativeScheduleMax_matchesSumOfPerEpochMax(t *testing.T) {
	// Cross-check the closed-form CumulativeScheduleMax against a naive per-epoch sum for a
	// range that spans every bracket boundary and a chunk of the tail.
	sum := math.ZeroInt()
	const upTo = 3660
	for e := uint64(1); e <= upTo; e++ {
		sum = sum.Add(types.MaxMintableForEpoch(e))
	}
	require.True(t, sum.Equal(types.CumulativeScheduleMax(upTo)))
}

func TestCumulativeScheduleMax_tailAccumulatesForever(t *testing.T) {
	at3651 := types.CumulativeScheduleMax(3651)
	at3652 := types.CumulativeScheduleMax(3652)
	require.True(t, at3652.Sub(at3651).Equal(oramaAmt(274)))
}

func TestCumulativeValidatorMinted_hardCodedValues(t *testing.T) {
	cases := []struct {
		name       string
		epoch      uint64
		wantNorama int64
	}{
		// Each value is 60% of the corresponding CumulativeScheduleMax total, hard-coded
		// independently rather than computed from SplitEpochMint/CumulativeScheduleMax here.
		{"zero epochs", 0, 0},
		{"one closed epoch (60% of 14,848 ORAMA)", 1, 8_908_800_000_000},
		{"one full bracket (60% of 730*14,848 ORAMA)", 730, 6_503_424_000_000_000},
		{"through the last halving epoch (60% of 21,000,640 ORAMA)", 3650, 12_600_384_000_000_000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := types.CumulativeValidatorMinted(tc.epoch)
			require.True(t, got.Equal(math.NewInt(tc.wantNorama)), "epoch %d: got %s, want %d", tc.epoch, got, tc.wantNorama)
		})
	}
}

func TestCumulativeValidatorMinted_equalsSixtyPercentOfScheduleMax(t *testing.T) {
	// This holds exactly (not approximately) because every schedule amount is divisible by 100 -
	// see CumulativeValidatorMinted's doc comment - and cross-checks the closed-form bracket
	// arithmetic against an independently expressed relationship, rather than re-running the same
	// per-epoch loop.
	for _, epoch := range []uint64{1, 730, 731, 1461, 2921, 3650, 3651, 4000, 10000} {
		scheduleMax := types.CumulativeScheduleMax(epoch)
		want := scheduleMax.MulRaw(60).QuoRaw(100)
		got := types.CumulativeValidatorMinted(epoch)
		require.True(t, got.Equal(want), "epoch %d: got %s, want %s", epoch, got, want)
	}
}
