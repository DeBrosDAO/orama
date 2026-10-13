package types_test

import (
	"testing"

	"cosmossdk.io/math"

	"github.com/DeBrosOfficial/network/chain/x/power/types"
)

func TestClampPublishedIncreases_whaleGainStopsAtOneThird(t *testing.T) {
	prev := map[string]math.LegacyDec{
		"committee": math.LegacyOneDec(),
		"whale":     math.LegacyZeroDec(),
	}
	desired := map[string]math.LegacyDec{
		"committee": math.LegacyMustNewDecFromStr("0.51"),
		"whale":     math.LegacyMustNewDecFromStr("0.49"),
	}
	got := types.ClampPublishedIncreases(prev, desired, types.MaxVotingPowerShiftPerEpoch())
	whale := got["whale"]
	if whale.IsNil() || whale.GT(types.MaxVotingPowerShiftPerEpoch()) {
		t.Fatalf("whale share = %s, want at most 1/3", whale)
	}
	shift := publishedShiftForTest(prev, got)
	if shift.GT(types.MaxVotingPowerShiftPerEpoch()) {
		t.Fatalf("shift = %s, want at most 1/3", shift)
	}
}

func TestClampPublishedIncreases_jailDropsImmediately(t *testing.T) {
	prev := map[string]math.LegacyDec{
		"jailed": math.LegacyMustNewDecFromStr("0.5"),
		"other":  math.LegacyMustNewDecFromStr("0.5"),
	}
	desired := map[string]math.LegacyDec{
		"other": math.LegacyOneDec(),
	}
	got := types.ClampPublishedIncreases(prev, desired, types.MaxVotingPowerShiftPerEpoch())
	if _, still := got["jailed"]; still {
		t.Fatalf("jailed validator kept share %s", got["jailed"])
	}
	other := got["other"]
	if other.IsNil() || !other.Equal(math.LegacyOneDec()) {
		t.Fatalf("other share = %s, want 1", other)
	}
}

func TestClampPublishedIncreases_smallMoveUnchanged(t *testing.T) {
	prev := map[string]math.LegacyDec{
		"a": math.LegacyMustNewDecFromStr("0.6"),
		"b": math.LegacyMustNewDecFromStr("0.4"),
	}
	desired := map[string]math.LegacyDec{
		"a": math.LegacyMustNewDecFromStr("0.55"),
		"b": math.LegacyMustNewDecFromStr("0.45"),
	}
	got := types.ClampPublishedIncreases(prev, desired, types.MaxVotingPowerShiftPerEpoch())
	if !got["b"].Equal(desired["b"]) {
		t.Fatalf("share = %s, want the full %s step", got["b"], desired["b"])
	}
}

func TestClampPublishedIncreases_disjointReplacementSeatsTheNewSet(t *testing.T) {
	prev := map[string]math.LegacyDec{"old": math.LegacyOneDec()}
	desired := map[string]math.LegacyDec{"new": math.LegacyOneDec()}
	got := types.ClampPublishedIncreases(prev, desired, types.MaxVotingPowerShiftPerEpoch())
	if !got["new"].Equal(math.LegacyOneDec()) {
		t.Fatalf("new share = %s, want the replacement seated at 1", got["new"])
	}
	if _, still := got["old"]; still {
		t.Fatalf("removed validator kept share %s", got["old"])
	}
}

func TestClampPublishedIncreases_repeatedStepsReachTarget(t *testing.T) {
	prev := map[string]math.LegacyDec{"whale": math.LegacyZeroDec(), "rest": math.LegacyOneDec()}
	target := map[string]math.LegacyDec{
		"whale": math.LegacyMustNewDecFromStr("0.9"),
		"rest":  math.LegacyMustNewDecFromStr("0.1"),
	}
	for i := 0; i < 6; i++ {
		prev = types.ClampPublishedIncreases(prev, target, types.MaxVotingPowerShiftPerEpoch())
	}
	if prev["whale"].LT(target["whale"]) {
		t.Fatalf("whale = %s after 6 steps, want %s", prev["whale"], target["whale"])
	}
}

func publishedShiftForTest(a, b map[string]math.LegacyDec) math.LegacyDec {
	seen := map[string]bool{}
	for addr := range a {
		seen[addr] = true
	}
	for addr := range b {
		seen[addr] = true
	}
	l1 := math.LegacyZeroDec()
	for addr := range seen {
		av, bv := math.LegacyZeroDec(), math.LegacyZeroDec()
		if v, ok := a[addr]; ok && !v.IsNil() {
			av = v
		}
		if v, ok := b[addr]; ok && !v.IsNil() {
			bv = v
		}
		delta := av.Sub(bv)
		if delta.IsNegative() {
			delta = delta.Neg()
		}
		l1 = l1.Add(delta)
	}
	return l1.QuoInt64(2)
}
