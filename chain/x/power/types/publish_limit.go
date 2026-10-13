package types

import "cosmossdk.io/math"

// ClampPublishedIncreases limits how far `desired` may move from `prev` in one
// block. Both maps are normalized voting-power shares keyed by operator address;
// a missing key is zero. Decreases, including a jailed, tombstoned, or unbonded
// validator falling to zero, apply immediately. Increases are scaled back until
// the total variation is at most maxShift. A decrease that itself exceeds
// maxShift is still published: keeping a jailed validator in the set to satisfy
// the light-client bound would leave it voting.
func ClampPublishedIncreases(prev, desired map[string]math.LegacyDec, maxShift math.LegacyDec) map[string]math.LegacyDec {
	if !publishedShift(prev, desired).GT(maxShift) {
		return desired
	}
	best := blendPublished(prev, desired, math.LegacyZeroDec())
	lo := math.LegacyZeroDec()
	hi := math.LegacyOneDec()
	for i := 0; i < 64; i++ {
		mid := lo.Add(hi).QuoInt64(2)
		trial := blendPublished(prev, desired, mid)
		if publishedShift(prev, trial).GT(maxShift) {
			hi = mid
			continue
		}
		best = trial
		lo = mid
	}
	// alpha 0 can sum to zero when the previous set and the desired set share
	// no address: every old validator decreased to zero and every new one is
	// an increase that alpha 0 refuses. Publishing that empty set halts the
	// chain. Seat the desired set instead.
	if !hasPositiveShare(best) && hasPositiveShare(desired) {
		return desired
	}
	return best
}

func hasPositiveShare(shares map[string]math.LegacyDec) bool {
	// nosemgrep: consensus-map-iteration -- answers whether any share is positive
	for _, share := range shares {
		if !share.IsNil() && share.IsPositive() {
			return true
		}
	}
	return false
}

// publishedShift is the total-variation distance between two normalized share maps.
func publishedShift(a, b map[string]math.LegacyDec) math.LegacyDec {
	seen := make(map[string]bool, len(a)+len(b))
	// nosemgrep: consensus-map-iteration -- builds a set of keys
	for addr := range a {
		seen[addr] = true
	}
	// nosemgrep: consensus-map-iteration -- builds a set of keys
	for addr := range b {
		seen[addr] = true
	}
	l1 := math.LegacyZeroDec()
	// nosemgrep: consensus-map-iteration -- sums LegacyDec values; decimal addition is exact, so the sum does not depend on the order
	for addr := range seen {
		delta := shareOrZero(a, addr).Sub(shareOrZero(b, addr))
		if delta.IsNegative() {
			delta = delta.Neg()
		}
		l1 = l1.Add(delta)
	}
	return l1.QuoInt64(2)
}

// blendPublished applies decreases in full and moves increases `alpha` of the way
// from prev to desired, then renormalizes. alpha is 0 or 1 at the endpoints.
func blendPublished(prev, desired map[string]math.LegacyDec, alpha math.LegacyDec) map[string]math.LegacyDec {
	seen := make(map[string]bool, len(prev)+len(desired))
	// nosemgrep: consensus-map-iteration -- builds a set of keys
	for addr := range prev {
		seen[addr] = true
	}
	// nosemgrep: consensus-map-iteration -- builds a set of keys
	for addr := range desired {
		seen[addr] = true
	}
	raw := make(map[string]math.LegacyDec, len(seen))
	sum := math.LegacyZeroDec()
	// nosemgrep: consensus-map-iteration -- fills a map from per-key values and sums them with exact LegacyDec addition
	for addr := range seen {
		before := shareOrZero(prev, addr)
		after := shareOrZero(desired, addr)
		value := after
		if after.GT(before) {
			value = before.Add(after.Sub(before).Mul(alpha))
		}
		raw[addr] = value
		sum = sum.Add(value)
	}
	if !sum.IsPositive() {
		return map[string]math.LegacyDec{}
	}
	out := make(map[string]math.LegacyDec, len(raw))
	// nosemgrep: consensus-map-iteration -- maps each key to a value computed from that key and the order-independent sum
	for addr, value := range raw {
		if !value.IsPositive() {
			continue
		}
		out[addr] = value.Quo(sum)
	}
	return out
}

func shareOrZero(shares map[string]math.LegacyDec, addr string) math.LegacyDec {
	value, ok := shares[addr]
	if !ok || value.IsNil() {
		return math.LegacyZeroDec()
	}
	return value
}
