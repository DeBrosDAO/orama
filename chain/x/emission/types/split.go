package types

import "cosmossdk.io/math"

// Emission split percentages (plans/open-network.md D13, track-c-chain.md C3): 60% validators
// and delegators, 25% storage, 10% relay, 5% development. Every share but the validator share is
// a ceiling: it is recorded (CeilingRecord) but never minted by this module.
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

// SplitEpochMint splits an epoch's maximum mintable amount into its four shares. Storage, relay
// and development are computed first by integer division (each share may lose up to 99 norama to
// truncation); the validator share is whatever is left, so no norama is ever lost and nothing
// beyond the validator share is minted here.
func SplitEpochMint(total math.Int) EpochSplit {
	storage := total.MulRaw(StorageSharePercent).QuoRaw(PercentDenominator)
	relay := total.MulRaw(RelaySharePercent).QuoRaw(PercentDenominator)
	development := total.MulRaw(DevelopmentSharePercent).QuoRaw(PercentDenominator)
	validator := total.Sub(storage).Sub(relay).Sub(development)

	return EpochSplit{
		Validator:   validator,
		Storage:     storage,
		Relay:       relay,
		Development: development,
	}
}
