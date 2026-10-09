package types

import (
	"fmt"

	"cosmossdk.io/math"
)

// Placeholder genesis defaults. G1 has not signed the relay numbers
// (plans/open-network/track-g-token-legal-security.md); a production genesis
// overrides them. Nothing in this module can change them later.
const (
	// DefaultMinReportersQuorum is all 3 initial dirauths (owner decision), so one lying reporter cannot move the median.
	DefaultMinReportersQuorum uint32 = 3
	// DefaultMinUptimeFraction pays a relay only when median uptime is at least 90%.
	DefaultMinUptimeFraction = "0.9"
	// DefaultExitMultiplier pays an exit twice its capped weight.
	DefaultExitMultiplier = "2"
	// MaxExitMultiplier bounds exit_multiplier so the multiplied weight stays far from overflow.
	MaxExitMultiplier int64 = 1000
	// DefaultPerRelayCap is 100 ORAMA of norama (1 ORAMA = 10^9 norama).
	DefaultPerRelayCap int64 = 100_000_000_000
	// DefaultPerOperatorCap is 200 ORAMA.
	DefaultPerOperatorCap int64 = 200_000_000_000
	// DefaultPerPrefix16Cap is 200 ORAMA.
	DefaultPerPrefix16Cap int64 = 200_000_000_000
)

// DefaultParams returns the placeholder genesis parameters.
func DefaultParams() Params {
	return Params{
		MinReportersQuorum: DefaultMinReportersQuorum,
		MinUptimeFraction:  math.LegacyMustNewDecFromStr(DefaultMinUptimeFraction),
		ExitMultiplier:     math.LegacyMustNewDecFromStr(DefaultExitMultiplier),
		PerRelayCap:        math.NewInt(DefaultPerRelayCap),
		PerOperatorCap:     math.NewInt(DefaultPerOperatorCap),
		PerPrefix16Cap:     math.NewInt(DefaultPerPrefix16Cap),
	}
}

// Validate checks Params for internal consistency.
func (p Params) Validate() error {
	if p.MinReportersQuorum == 0 {
		return fmt.Errorf("min_reporters_quorum must be at least 1")
	}
	if p.MinUptimeFraction.IsNil() || p.MinUptimeFraction.IsNegative() || p.MinUptimeFraction.GT(math.LegacyOneDec()) {
		return fmt.Errorf("min_uptime_fraction must be in [0, 1], got %s", p.MinUptimeFraction)
	}
	if p.ExitMultiplier.IsNil() || p.ExitMultiplier.LT(math.LegacyOneDec()) || p.ExitMultiplier.GT(math.LegacyNewDec(MaxExitMultiplier)) {
		return fmt.Errorf("exit_multiplier must be in [1, %d], got %s", MaxExitMultiplier, p.ExitMultiplier)
	}
	if err := positiveInt("per_relay_cap", p.PerRelayCap); err != nil {
		return err
	}
	if err := positiveInt("per_operator_cap", p.PerOperatorCap); err != nil {
		return err
	}
	if err := positiveInt("per_prefix16_cap", p.PerPrefix16Cap); err != nil {
		return err
	}
	return nil
}

func positiveInt(name string, v math.Int) error {
	if v.IsNil() || !v.IsPositive() {
		return fmt.Errorf("%s must be a positive integer, got %s", name, v)
	}
	return nil
}
