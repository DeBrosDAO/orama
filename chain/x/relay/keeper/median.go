package keeper

import (
	"sort"

	"cosmossdk.io/math"

	"github.com/DeBrosOfficial/network/chain/x/relay/types"
)

// medianInt is the lower median of an odd count and the floored average of
// the two middle values of an even count. A single value is itself, so quorum
// 1 pays that reporter's weight.
func medianInt(vals []math.Int) math.Int {
	sorted := append([]math.Int(nil), vals...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].LT(sorted[j]) })
	n := len(sorted)
	if n%2 == 1 {
		return sorted[n/2]
	}
	return sorted[n/2-1].Add(sorted[n/2]).QuoRaw(2)
}

func medianDec(vals []math.LegacyDec) math.LegacyDec {
	sorted := append([]math.LegacyDec(nil), vals...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].LT(sorted[j]) })
	n := len(sorted)
	if n%2 == 1 {
		return sorted[n/2]
	}
	return sorted[n/2-1].Add(sorted[n/2]).Quo(math.LegacyNewDec(2))
}

// medianExit is the lower median of the Exit bit: set only when the middle
// value (odd) or the lower of the two middle values (even) is set. One
// dissenting reporter in a set of three does not set it.
func medianExit(flags []uint32) bool {
	bits := make([]int, len(flags))
	for i, flag := range flags {
		if flag&types.FlagExit != 0 {
			bits[i] = 1
		}
	}
	sort.Ints(bits)
	return bits[(len(bits)-1)/2] == 1
}
