package types

import (
	"fmt"
	"time"

	"cosmossdk.io/math"

	"github.com/DeBrosOfficial/network/chain/app/params"
)

// MaxAdvanceAttempts is how many blocks in a row advancing one proposal may fail before the
// proposal is closed as FAILED. It is a constant, not a parameter: it only bounds how long a
// proposal that can never be tallied stays active, and no governance vote should tune it.
const MaxAdvanceAttempts uint32 = 5

const (
	// DefaultVotingPeriod is one week. Bounds keep a vote from being shorter
	// than a day or longer than four weeks.
	DefaultVotingPeriod = 7 * 24 * time.Hour
	minVotingPeriod     = 24 * time.Hour
	maxVotingPeriod     = 28 * 24 * time.Hour

	// DefaultHouseBondOrama is the genesis house bond, in whole ORAMA.
	// G1 has not signed a different number off; the floor is 1 ORAMA so a
	// parameter vote cannot make eligibility free.
	DefaultHouseBondOrama int64 = 1_000
	minHouseBondOrama     int64 = 1
	maxHouseBondOrama     int64 = 1_000_000

	defaultMaxPerPrefix16 uint32 = 3
	defaultMaxPerASN      uint32 = 5
	minNetworkCap         uint32 = 1
	maxNetworkCap         uint32 = 21

	// DefaultMinHouseSize is the eligible-operator count both tiers require
	// (D17, C5). The floor is the plan value, so a genesis cannot open
	// governance to a smaller house.
	DefaultMinHouseSize uint32 = 21
	minMinHouseSize     uint32 = 21
	maxMinHouseSize     uint32 = 101
)

// Execution delays (D17: 14 days for parameters, 60 for upgrades, 7 for
// spends, no expedited proposals) and the veto window (D17: 7 days). Each
// floor is the plan value; a genesis may lengthen a delay but never shorten
// it.
const (
	DefaultVetoWindow        = 7 * 24 * time.Hour
	minVetoWindow            = 7 * 24 * time.Hour
	maxVetoWindow            = 28 * 24 * time.Hour
	DefaultParameterTimelock = 14 * 24 * time.Hour
	minParameterTimelock     = 14 * 24 * time.Hour
	maxParameterTimelock     = 60 * 24 * time.Hour
	DefaultUpgradeTimelock   = 60 * 24 * time.Hour
	minUpgradeTimelock       = 60 * 24 * time.Hour
	maxUpgradeTimelock       = 180 * 24 * time.Hour
	DefaultSpendTimelock     = 7 * 24 * time.Hour
	minSpendTimelock         = 7 * 24 * time.Hour
	maxSpendTimelock         = 30 * 24 * time.Hour
)

var (
	minTokenQuorum = math.LegacyMustNewDecFromStr("0.334")
	maxTokenQuorum = math.LegacyMustNewDecFromStr("0.667")

	// DefaultTokenQuorum is 40% of bonded stake.
	DefaultTokenQuorum = math.LegacyMustNewDecFromStr("0.4")

	minPassThreshold = math.LegacyMustNewDecFromStr("0.5")
	maxPassThreshold = math.LegacyMustNewDecFromStr("0.667")

	// DefaultTokenPassThreshold is a strict majority of the weight that voted.
	// yes == no still fails the separate yes > no check.
	DefaultTokenPassThreshold = math.LegacyMustNewDecFromStr("0.5")
)

// DefaultBootstrapExitStake is 271000 ORAMA, P1 in plans/open-network.md.
func DefaultBootstrapExitStake() math.Int {
	return math.NewInt(271_000).MulRaw(params.NoramaPerOrama)
}

// DefaultHouseBond is DefaultHouseBondOrama in norama.
func DefaultHouseBond() math.Int {
	return math.NewInt(DefaultHouseBondOrama).MulRaw(params.NoramaPerOrama)
}

// DefaultParams returns the genesis parameters. Both tiers still start closed:
// opening depends on live stake, lambda and the operator house, not on these
// numbers alone.
func DefaultParams() Params {
	return Params{
		BootstrapExitStake:     DefaultBootstrapExitStake(),
		TokenQuorum:            DefaultTokenQuorum,
		TokenPassThreshold:     DefaultTokenPassThreshold,
		VotingPeriodSeconds:    int64(DefaultVotingPeriod / time.Second),
		HouseBond:              DefaultHouseBond(),
		MaxEligiblePerPrefix16: defaultMaxPerPrefix16,
		MaxEligiblePerAsn:      defaultMaxPerASN,

		MinHouseSize:             DefaultMinHouseSize,
		VetoWindowSeconds:        int64(DefaultVetoWindow / time.Second),
		ParameterTimelockSeconds: int64(DefaultParameterTimelock / time.Second),
		UpgradeTimelockSeconds:   int64(DefaultUpgradeTimelock / time.Second),
		SpendTimelockSeconds:     int64(DefaultSpendTimelock / time.Second),
	}
}

// Validate checks coded bounds. bootstrap_exit_stake may be zero (the stake
// side of the parameter tier is then already met) but not negative.
func (p Params) Validate() error {
	if p.BootstrapExitStake.IsNil() || p.BootstrapExitStake.IsNegative() {
		return fmt.Errorf("bootstrap_exit_stake must be a non-negative integer")
	}
	if err := validateDecRange("token_quorum", p.TokenQuorum, minTokenQuorum, maxTokenQuorum); err != nil {
		return err
	}
	if err := validateDecRange("token_pass_threshold", p.TokenPassThreshold, minPassThreshold, maxPassThreshold); err != nil {
		return err
	}
	if p.VotingPeriodSeconds < int64(minVotingPeriod/time.Second) || p.VotingPeriodSeconds > int64(maxVotingPeriod/time.Second) {
		return fmt.Errorf("voting_period_seconds must be in [%d, %d], got %d", int64(minVotingPeriod/time.Second), int64(maxVotingPeriod/time.Second), p.VotingPeriodSeconds)
	}
	if p.HouseBond.IsNil() || p.HouseBond.LT(math.NewInt(minHouseBondOrama).MulRaw(params.NoramaPerOrama)) || p.HouseBond.GT(math.NewInt(maxHouseBondOrama).MulRaw(params.NoramaPerOrama)) {
		return fmt.Errorf("house_bond must be in [%d, %d] ORAMA, got %s norama", minHouseBondOrama, maxHouseBondOrama, p.HouseBond)
	}
	if p.MaxEligiblePerPrefix16 < minNetworkCap || p.MaxEligiblePerPrefix16 > maxNetworkCap {
		return fmt.Errorf("max_eligible_per_prefix16 must be in [%d, %d], got %d", minNetworkCap, maxNetworkCap, p.MaxEligiblePerPrefix16)
	}
	if p.MaxEligiblePerAsn < minNetworkCap || p.MaxEligiblePerAsn > maxNetworkCap {
		return fmt.Errorf("max_eligible_per_asn must be in [%d, %d], got %d", minNetworkCap, maxNetworkCap, p.MaxEligiblePerAsn)
	}
	return p.validateGovernanceTiming()
}

// validateGovernanceTiming checks the genesis-fixed house size and delays.
func (p Params) validateGovernanceTiming() error {
	if p.MinHouseSize < minMinHouseSize || p.MinHouseSize > maxMinHouseSize {
		return fmt.Errorf("min_house_size must be in [%d, %d], got %d", minMinHouseSize, maxMinHouseSize, p.MinHouseSize)
	}
	seconds := []struct {
		name     string
		v        int64
		min, max time.Duration
	}{
		{"veto_window_seconds", p.VetoWindowSeconds, minVetoWindow, maxVetoWindow},
		{"parameter_timelock_seconds", p.ParameterTimelockSeconds, minParameterTimelock, maxParameterTimelock},
		{"upgrade_timelock_seconds", p.UpgradeTimelockSeconds, minUpgradeTimelock, maxUpgradeTimelock},
		{"spend_timelock_seconds", p.SpendTimelockSeconds, minSpendTimelock, maxSpendTimelock},
	}
	for _, s := range seconds {
		if s.v < int64(s.min/time.Second) || s.v > int64(s.max/time.Second) {
			return fmt.Errorf("%s must be in [%d, %d], got %d", s.name, int64(s.min/time.Second), int64(s.max/time.Second), s.v)
		}
	}
	return nil
}

// VetoWindow is VetoWindowSeconds as a duration.
func (p Params) VetoWindow() time.Duration {
	return time.Duration(p.VetoWindowSeconds) * time.Second
}

// TimelockFor is the delay after content passes. Spends use the spend delay,
// parameters the parameter delay, every other structural action the upgrade
// delay. No path is shorter.
func (p Params) TimelockFor(c ProposalContent) time.Duration {
	switch {
	case c.ParameterChange != nil:
		return time.Duration(p.ParameterTimelockSeconds) * time.Second
	case c.DevelopmentSpend != nil:
		return time.Duration(p.SpendTimelockSeconds) * time.Second
	default:
		return time.Duration(p.UpgradeTimelockSeconds) * time.Second
	}
}

func validateDecRange(name string, v, min, max math.LegacyDec) error {
	if v.IsNil() || v.LT(min) || v.GT(max) {
		return fmt.Errorf("%s must be in [%s, %s], got %s", name, min, max, v)
	}
	return nil
}

// VotingPeriod is VotingPeriodSeconds as a duration.
func (p Params) VotingPeriod() time.Duration {
	return time.Duration(p.VotingPeriodSeconds) * time.Second
}
