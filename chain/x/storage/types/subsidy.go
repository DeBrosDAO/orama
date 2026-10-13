package types

import (
	"sort"

	"cosmossdk.io/math"
)

const (
	// ServiceProviderBPS is the provider's share of a service payment (C2: 90%).
	ServiceProviderBPS int64 = 9_000
	// ServiceBurnBPS is the burned share (5%).
	ServiceBurnBPS int64 = 500
	// ServiceArchiveBPS is the archive-fund share (5%).
	ServiceArchiveBPS int64 = 500
	// ServiceBPSBase is the denominator of the shares above.
	ServiceBPSBase int64 = 10_000
)

// sharesCoverPayment is a compile-time check that 90+5+5 == 100.
var _ = [1]struct{}{}[ServiceProviderBPS+ServiceBurnBPS+ServiceArchiveBPS-ServiceBPSBase]

// SMax is the hard-coded subsidy ceiling. No parameter can raise s above it.
var SMax = math.LegacyOneDec()

// MintKind distinguishes a capped PRIVATE subsidy from a protocol payment.
type MintKind int

const (
	// MintSubsidy is s × price on a user PRIVATE deal, capped per operator.
	MintSubsidy MintKind = iota
	// MintProtocol is a protocol PUBLIC_PIN payment, paid from the ceiling.
	MintProtocol
	// MintArchive is a protocol ARCHIVE payment. The ceiling pays first and
	// the archive fund tops up the remainder.
	MintArchive
)

// MintClaim is one proved slot's claim on the storage ceiling.
type MintClaim struct {
	Operator  string
	Kind      MintKind
	Amount    math.Int
	FullPrice math.Int
}

// SubsidyRate is the fraction s of PRIVATE price paid from the storage ceiling.
// It is 0 until sMin distinct operators are active, then ramps linearly to SMax
// at sFull. The result is never greater than SMax.
func SubsidyRate(active, sMin, sFull uint64) math.LegacyDec {
	if active < sMin || sMin == 0 {
		return math.LegacyZeroDec()
	}
	if sFull < sMin {
		sFull = sMin
	}
	if active >= sFull {
		return SMax
	}
	num := active - sMin + 1
	den := sFull - sMin + 1
	rate := math.LegacyNewDec(int64(num)).Quo(math.LegacyNewDec(int64(den)))
	if rate.GT(SMax) {
		return SMax
	}
	return rate
}

// OperatorSubsidyCap is one operator's maximum subsidy in an epoch:
// ceiling / s_min_providers. Capacity above that share cannot collect more.
func OperatorSubsidyCap(ceiling math.Int, sMin uint64) math.Int {
	if sMin == 0 || ceiling.IsNil() || !ceiling.IsPositive() {
		return math.ZeroInt()
	}
	return ceiling.QuoRaw(int64(sMin))
}

// ApplyRate returns amount × rate, truncated toward zero. A zero rate yields zero.
func ApplyRate(amount math.Int, rate math.LegacyDec) math.Int {
	if amount.IsNil() || !amount.IsPositive() || rate.IsNil() || !rate.IsPositive() {
		return math.ZeroInt()
	}
	return amount.ToLegacyDec().Mul(rate).TruncateInt()
}

// EpochSubsidyDemand is one proved slot's subsidy for the epoch being settled.
// extraExtensionEpochs does not multiply it: an extension escrows future epochs
// and those epochs are settled when they are proved, not in advance.
func EpochSubsidyDemand(price math.Int, rate math.LegacyDec, extraExtensionEpochs uint64) math.Int {
	if extraExtensionEpochs > 0 {
		return ApplyRate(price, rate)
	}
	return ApplyRate(price, rate)
}

// SplitServicePayment splits a provider payment 90/5/5. The provider receives
// the rounding remainder so the three parts sum to amount exactly.
func SplitServicePayment(amount math.Int) (toProvider, burn, archive math.Int) {
	zero := math.ZeroInt()
	if amount.IsNil() || !amount.IsPositive() {
		return zero, zero, zero
	}
	burn = amount.MulRaw(ServiceBurnBPS).QuoRaw(ServiceBPSBase)
	archive = amount.MulRaw(ServiceArchiveBPS).QuoRaw(ServiceBPSBase)
	toProvider = amount.Sub(burn).Sub(archive)
	return toProvider, burn, archive
}

// ScaleToSum scales amounts so they sum to min(sum, limit). A non-positive
// limit yields zeros. When the sum already fits, the amounts are copied.
func ScaleToSum(amounts []math.Int, limit math.Int) []math.Int {
	out := make([]math.Int, len(amounts))
	for i := range out {
		out[i] = math.ZeroInt()
	}
	sum := math.ZeroInt()
	clean := make([]math.Int, len(amounts))
	for i, a := range amounts {
		if a.IsNil() || a.IsNegative() {
			clean[i] = math.ZeroInt()
		} else {
			clean[i] = a
		}
		sum = sum.Add(clean[i])
	}
	if len(amounts) == 0 || limit.IsNil() || !limit.IsPositive() || sum.IsZero() {
		return out
	}
	if !sum.GT(limit) {
		copy(out, clean)
		return out
	}
	type frac struct {
		idx int
		rem math.Int
	}
	fracs := make([]frac, len(clean))
	allocated := math.ZeroInt()
	for i, a := range clean {
		prod := a.Mul(limit)
		out[i] = prod.Quo(sum)
		fracs[i] = frac{idx: i, rem: prod.Sub(out[i].Mul(sum))}
		allocated = allocated.Add(out[i])
	}
	left := limit.Sub(allocated)
	sort.Slice(fracs, func(i, j int) bool {
		if fracs[i].rem.Equal(fracs[j].rem) {
			return fracs[i].idx < fracs[j].idx
		}
		return fracs[i].rem.GT(fracs[j].rem)
	})
	for i := 0; left.IsPositive(); i++ {
		idx := fracs[i%len(fracs)].idx
		out[idx] = out[idx].Add(math.OneInt())
		left = left.Sub(math.OneInt())
	}
	return out
}

// ComputeMintPlan applies the per-operator subsidy cap, then pro-rates every
// ceiling claim (subsidies and protocol payments) down to the epoch ceiling.
// ARCHIVE claims also record the shortfall the archive fund may top up.
// The returned slices are aligned with claims. Sum of mints is at most ceiling.
func ComputeMintPlan(claims []MintClaim, ceiling, perOperatorCap math.Int) (mints, topUps []math.Int) {
	n := len(claims)
	mints = make([]math.Int, n)
	topUps = make([]math.Int, n)
	if ceiling.IsNil() || ceiling.IsNegative() {
		ceiling = math.ZeroInt()
	}
	if perOperatorCap.IsNil() || perOperatorCap.IsNegative() {
		perOperatorCap = math.ZeroInt()
	}
	groups := map[string][]int{}
	var ops []string
	for i, c := range claims {
		amt := c.Amount
		if amt.IsNil() || amt.IsNegative() {
			amt = math.ZeroInt()
		}
		mints[i] = amt
		if c.Kind != MintSubsidy {
			continue
		}
		if _, ok := groups[c.Operator]; !ok {
			ops = append(ops, c.Operator)
		}
		groups[c.Operator] = append(groups[c.Operator], i)
	}
	for _, op := range ops {
		idxs := groups[op]
		amts := make([]math.Int, len(idxs))
		for j, i := range idxs {
			amts[j] = mints[i]
		}
		scaled := ScaleToSum(amts, perOperatorCap)
		for j, i := range idxs {
			mints[i] = scaled[j]
		}
	}
	mints = ScaleToSum(mints, ceiling)
	for i, c := range claims {
		if c.Kind != MintArchive {
			continue
		}
		full := c.FullPrice
		if full.IsNil() || !full.IsPositive() {
			continue
		}
		got := mints[i]
		if got.IsNil() {
			got = math.ZeroInt()
		}
		if got.LT(full) {
			topUps[i] = full.Sub(got)
		}
	}
	return mints, topUps
}
