package types

import (
	"context"
	"fmt"

	"cosmossdk.io/math"
)

// Emission split percentages (plans/open-network.md D13, track-c-chain.md C3): 60% validators
// and delegators, 25% storage, 10% relay, 5% development. closeEpoch mints only the validator
// share. Storage and relay stay ceilings. The development share stays a ceiling until
// Keeper.MintDevelopmentSpend mints an approved amount, and never more than that epoch's
// development share minus what was already minted.
const (
	ValidatorSharePercent   = 60
	StorageSharePercent     = 25
	RelaySharePercent       = 10
	DevelopmentSharePercent = 5

	// PercentDenominator is what every share percent above is out of.
	PercentDenominator = 100
)

// sharesSumToWhole is a compile-time assertion that the four shares above sum to exactly
// PercentDenominator. Divides-by-zero (a compile error) if they ever don't.
var _ = [1]struct{}{}[ValidatorSharePercent+StorageSharePercent+RelaySharePercent+DevelopmentSharePercent-PercentDenominator]

// CeilingWindow is how many past epochs' CeilingRecord entries x/emission keeps before pruning
// the oldest one (plans/open-network/track-c-chain.md C3: "a bounded window"). It lives here
// (rather than in the keeper package) so genesis validation can check a ceiling record's epoch is
// within the window without a keeper->types import cycle.
const CeilingWindow = 30

// EpochSplit is one epoch's maximum split into its four shares.
type EpochSplit struct {
	// Validator is the amount x/emission actually mints and forwards to the fee collector this
	// epoch. It always absorbs the remainder left over from the other three shares' integer
	// division, so Validator+Storage+Relay+Development always sums exactly to the input total.
	Validator   math.Int
	Storage     math.Int
	Relay       math.Int
	Development math.Int
}

// SplitBoundPoints is how far a structural vote may move each share from its canonical
// percentage (track-c-chain.md C3: "Each share can move by at most ±10 points").
const SplitBoundPoints = 10

// SplitPercents is the four shares as whole percentages summing to PercentDenominator.
type SplitPercents struct {
	Validator   uint32
	Storage     uint32
	Relay       uint32
	Development uint32
}

// CanonicalSplitPercents is the genesis 60/25/10/5 split.
func CanonicalSplitPercents() SplitPercents {
	return SplitPercents{
		Validator:   ValidatorSharePercent,
		Storage:     StorageSharePercent,
		Relay:       RelaySharePercent,
		Development: DevelopmentSharePercent,
	}
}

// IsCanonical reports whether p is the canonical split.
func (p SplitPercents) IsCanonical() bool { return p == CanonicalSplitPercents() }

// Validate checks that p sums to 100 and that each share is within SplitBoundPoints of its
// canonical value.
func (p SplitPercents) Validate() error {
	if sum := p.Validator + p.Storage + p.Relay + p.Development; sum != PercentDenominator {
		return fmt.Errorf("emission split must sum to %d, got %d", PercentDenominator, sum)
	}
	canonical := CanonicalSplitPercents()
	checks := []struct {
		name      string
		got, base uint32
	}{
		{"validator_percent", p.Validator, canonical.Validator},
		{"storage_percent", p.Storage, canonical.Storage},
		{"relay_percent", p.Relay, canonical.Relay},
		{"development_percent", p.Development, canonical.Development},
	}
	for _, c := range checks {
		low := int(c.base) - SplitBoundPoints
		if low < 0 {
			low = 0
		}
		if int(c.got) < low || int(c.got) > int(c.base)+SplitBoundPoints {
			return fmt.Errorf("%s must stay within %d points of %d, got %d", c.name, SplitBoundPoints, c.base, c.got)
		}
	}
	return nil
}

// SplitEpochMint splits an epoch's maximum mintable amount into its four shares at the
// canonical 60/25/10/5 percentages.
func SplitEpochMint(total math.Int) EpochSplit {
	return SplitEpochMintAt(total, CanonicalSplitPercents())
}

// SplitEpochMintAt splits total at pct. Storage, relay and development are computed first by
// integer division (each share may lose up to 99 norama to truncation); the validator share is
// whatever is left, so no norama is ever lost and nothing beyond the validator share is minted
// here. pct must already be valid (SplitPercents.Validate).
func SplitEpochMintAt(total math.Int, pct SplitPercents) EpochSplit {
	storage := total.MulRaw(int64(pct.Storage)).QuoRaw(PercentDenominator)
	relay := total.MulRaw(int64(pct.Relay)).QuoRaw(PercentDenominator)
	development := total.MulRaw(int64(pct.Development)).QuoRaw(PercentDenominator)
	validator := total.Sub(storage).Sub(relay).Sub(development)

	return EpochSplit{
		Validator:   validator,
		Storage:     storage,
		Relay:       relay,
		Development: development,
	}
}

// SplitSource supplies the split in force. x/emission never stores it: x/houses does, after a
// structural proposal passes its timelock (plans/open-network.md D13, C5). A source with no
// enacted split returns CanonicalSplitPercents.
type SplitSource interface {
	EmissionSplit(ctx context.Context) (SplitPercents, error)
}

// Percents returns the split a ceiling record closed under. A record whose four percentages are
// all zero closed under the canonical split.
func (r CeilingRecord) Percents() SplitPercents {
	p := SplitPercents{
		Validator:   r.ValidatorPercent,
		Storage:     r.StoragePercent,
		Relay:       r.RelayPercent,
		Development: r.DevelopmentPercent,
	}
	if p == (SplitPercents{}) {
		return CanonicalSplitPercents()
	}
	return p
}
