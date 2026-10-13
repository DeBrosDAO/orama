package types_test

import (
	"fmt"
	"math/rand"
	"testing"

	"cosmossdk.io/math"

	"github.com/DeBrosOfficial/network/chain/x/power/types"
)

// TestComputeCappedShares_randomizedSplitBelowCapGainsNothing checks the C4
// property on random stake sets: an identity that is itself under the cap,
// split into smaller identities that are also under the cap, does not receive
// a larger combined share. Other validators in the set are large enough to be
// capped, so the result depends on redistribution and not on a two-party split.
func TestComputeCappedShares_randomizedSplitBelowCapGainsNothing(t *testing.T) {
	rng := rand.New(rand.NewSource(20260928))
	capFraction := dec("0.05")
	dust := math.LegacyNewDecWithPrec(1, 9)
	// The 2x redistribution bound is what keeps a below-cap split from collecting
	// more than the unsplit identity. An unbounded multiplier lets each new
	// identity absorb excess up to the cap, which is a different attack.
	multiplier := math.LegacyNewDec(2)

	for trial := 0; trial < 40; trial++ {
		subject := int64(100 + rng.Intn(50))
		whale := subject * 80
		others := make([]types.ValidatorStake, 0, 20)
		others = append(others, types.ValidatorStake{OperatorAddress: "whale", BondedTokens: math.NewInt(whale)})
		for i := 0; i < 18; i++ {
			others = append(others, types.ValidatorStake{
				OperatorAddress: fmt.Sprintf("minnow%d", i),
				BondedTokens:    math.NewInt(int64(20 + rng.Intn(40))),
			})
		}
		parts := splitPositive(rng, subject, 2+rng.Intn(2))

		unsplit := append([]types.ValidatorStake{{OperatorAddress: "subject", BondedTokens: math.NewInt(subject)}}, others...)
		unsplitShares := types.ComputeCappedShares(unsplit, capFraction, multiplier)
		if unsplitShares[0].GT(capFraction) {
			t.Fatalf("trial %d: subject share %s is above the cap; the fixture is wrong", trial, unsplitShares[0])
		}
		splitStakes := make([]types.ValidatorStake, 0, len(parts)+len(others))
		for i, part := range parts {
			splitStakes = append(splitStakes, types.ValidatorStake{
				OperatorAddress: fmt.Sprintf("part%d", i),
				BondedTokens:    math.NewInt(part),
			})
		}
		splitStakes = append(splitStakes, others...)
		splitShares := types.ComputeCappedShares(splitStakes, capFraction, multiplier)

		combined := math.LegacyZeroDec()
		for i := range parts {
			combined = combined.Add(splitShares[i])
		}
		if combined.Sub(unsplitShares[0]).GT(dust) {
			t.Fatalf("trial %d: split gained share, whole=%s combined=%s parts=%v",
				trial, unsplitShares[0], combined, parts)
		}
	}
}

// splitPositive returns n positive int64s that sum to total.
func splitPositive(rng *rand.Rand, total int64, n int) []int64 {
	if n < 1 {
		n = 1
	}
	out := make([]int64, n)
	remaining := total
	for i := 0; i < n-1; i++ {
		// Leave at least 1 for every later part.
		max := remaining - int64(n-1-i)
		out[i] = 1 + rng.Int63n(max)
		remaining -= out[i]
	}
	out[n-1] = remaining
	return out
}
