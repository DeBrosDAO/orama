package keeper

import (
	"fmt"
	"sort"

	"cosmossdk.io/math"
)

// scaleToCap returns amounts unchanged when their sum is within cap, or scales
// them so the sum equals cap. Remainder units from integer division go to the
// largest fractional parts. The result never exceeds cap.
func scaleToCap(amounts []math.Int, cap math.Int) ([]math.Int, error) {
	if cap.IsNil() || cap.IsNegative() {
		return nil, fmt.Errorf("cap must be non-negative, got %s", cap)
	}
	total := math.ZeroInt()
	for _, amount := range amounts {
		if amount.IsNil() || amount.IsNegative() {
			return nil, fmt.Errorf("amount must be non-negative, got %s", amount)
		}
		total = total.Add(amount)
	}
	if !total.IsPositive() || !total.GT(cap) {
		return append([]math.Int(nil), amounts...), nil
	}

	out := make([]math.Int, len(amounts))
	type part struct {
		index int
		frac  math.Int
	}
	parts := make([]part, len(amounts))
	distributed := math.ZeroInt()
	for i, amount := range amounts {
		product := amount.Mul(cap)
		out[i] = product.Quo(total)
		distributed = distributed.Add(out[i])
		parts[i] = part{index: i, frac: product.Mod(total)}
	}
	sort.Slice(parts, func(i, j int) bool {
		if !parts[i].frac.Equal(parts[j].frac) {
			return parts[i].frac.GT(parts[j].frac)
		}
		return parts[i].index < parts[j].index
	})

	remainder := cap.Sub(distributed)
	if remainder.IsNegative() {
		return nil, fmt.Errorf("pro rata overshot cap by %s", remainder.Neg())
	}
	for i := 0; remainder.IsPositive(); i++ {
		if i >= len(parts) || parts[i].frac.IsZero() {
			return nil, fmt.Errorf("pro rata remainder %s has no recipient", remainder)
		}
		out[parts[i].index] = out[parts[i].index].AddRaw(1)
		remainder = remainder.SubRaw(1)
	}

	got := math.ZeroInt()
	for _, amount := range out {
		got = got.Add(amount)
	}
	if !got.Equal(cap) {
		return nil, fmt.Errorf("pro rata summed to %s, want %s", got, cap)
	}
	return out, nil
}
