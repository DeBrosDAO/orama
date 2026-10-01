package types_test

import (
	"testing"

	"cosmossdk.io/math"

	"github.com/DeBrosOfficial/network/chain/x/fees/types"
)

func TestNextBaseFee_unchangedAtExactlyTarget(t *testing.T) {
	p := types.DefaultParams()
	current := math.NewInt(1_000)
	got := types.NextBaseFee(current, 50, 100, p) // 50/100 = 50% = target
	if !got.Equal(current) {
		t.Fatalf("NextBaseFee at exactly target = %s, want unchanged %s", got, current)
	}
}

func TestNextBaseFee_risesWhenFull(t *testing.T) {
	p := types.DefaultParams()
	current := math.NewInt(1_000)
	got := types.NextBaseFee(current, 100, 100, p) // 100% full: (100-50)/50 = 1.0, capped to 0.125
	want := math.NewInt(1_125)                     // 1000 * 1.125
	if !got.Equal(want) {
		t.Fatalf("NextBaseFee at 100%% full = %s, want %s (capped at +12.5%%)", got, want)
	}
}

func TestNextBaseFee_fallsWhenEmpty(t *testing.T) {
	p := types.DefaultParams()
	current := math.NewInt(1_000)
	got := types.NextBaseFee(current, 0, 100, p) // 0% full: (0-50)/50 = -1.0, capped to -0.125
	want := math.NewInt(875)                     // 1000 * 0.875
	if !got.Equal(want) {
		t.Fatalf("NextBaseFee at 0%% full = %s, want %s (capped at -12.5%%)", got, want)
	}
}

func TestNextBaseFee_neverBelowFloor(t *testing.T) {
	p := types.DefaultParams()
	p.MinBaseFee = math.NewInt(900)
	current := math.NewInt(950)
	got := types.NextBaseFee(current, 0, 100, p) // would drop to 831.25, floored to 900
	if !got.Equal(math.NewInt(900)) {
		t.Fatalf("NextBaseFee = %s, want floored at 900", got)
	}
}

func TestNextBaseFee_smallMoveWithinBounds(t *testing.T) {
	p := types.DefaultParams()
	current := math.NewInt(1_000_000)
	// 60% full: delta = (60-50)/50 = 0.2, capped to 0.125 (max change), so it still caps.
	got := types.NextBaseFee(current, 60, 100, p)
	want := math.NewInt(1_125_000)
	if !got.Equal(want) {
		t.Fatalf("NextBaseFee at 60%% full = %s, want %s", got, want)
	}
	// 55% full: delta = (55-50)/50 = 0.1, under the 0.125 cap, so it applies exactly.
	got2 := types.NextBaseFee(current, 55, 100, p)
	want2 := math.NewInt(1_100_000)
	if !got2.Equal(want2) {
		t.Fatalf("NextBaseFee at 55%% full = %s, want %s", got2, want2)
	}
}

func TestNextBaseFee_noBlockLimitLeavesFeeUnchanged(t *testing.T) {
	p := types.DefaultParams()
	current := math.NewInt(1_234)
	got := types.NextBaseFee(current, 999, 0, p)
	if !got.Equal(current) {
		t.Fatalf("NextBaseFee with gasLimit=0 = %s, want unchanged %s", got, current)
	}
}

func TestSplitDeposit_exactSum(t *testing.T) {
	p := types.DefaultParams() // 99%/1%
	refund, burn := types.SplitDeposit(math.NewInt(1_000), p)
	if !refund.Equal(math.NewInt(990)) {
		t.Fatalf("refund = %s, want 990", refund)
	}
	if !burn.Equal(math.NewInt(10)) {
		t.Fatalf("burn = %s, want 10", burn)
	}
	if !refund.Add(burn).Equal(math.NewInt(1_000)) {
		t.Fatalf("refund+burn = %s, want exactly 1000", refund.Add(burn))
	}
}

func TestSplitDeposit_roundingRemainderGoesToBurn(t *testing.T) {
	p := types.DefaultParams()
	// 101 * 0.99 = 99.99 -> truncates to 99; burn absorbs the remaining 2, not 1.
	refund, burn := types.SplitDeposit(math.NewInt(101), p)
	if !refund.Equal(math.NewInt(99)) {
		t.Fatalf("refund = %s, want 99", refund)
	}
	if !refund.Add(burn).Equal(math.NewInt(101)) {
		t.Fatalf("refund+burn = %s, want exactly 101", refund.Add(burn))
	}
}
