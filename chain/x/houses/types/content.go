package types

import (
	"fmt"
	"regexp"
	"time"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// Kind is which house rules a proposal uses.
type Kind int

const (
	// KindNone is an empty ProposalContent.
	KindNone Kind = iota
	// KindParameter is the token house plus an operator veto.
	KindParameter
	// KindStructural is both houses, including development spends.
	KindStructural
)

var (
	upgradeNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	codeHashPattern    = regexp.MustCompile(`^[0-9a-f]{64}$`)
	adapterPattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

	mMinDec = math.LegacyMustNewDecFromStr(MMin)
	mMaxDec = math.LegacyMustNewDecFromStr(MMax)
)

// ValidateBasic checks that exactly one action is set and that its fields are
// inside coded bounds. It does not know the current block height.
func (c ProposalContent) ValidateBasic() error {
	n := 0
	if c.ParameterChange != nil {
		n++
		if err := validateParameterChange(*c.ParameterChange); err != nil {
			return err
		}
	}
	if c.SoftwareUpgrade != nil {
		n++
		if err := validateUpgrade(*c.SoftwareUpgrade); err != nil {
			return err
		}
	}
	if c.EmissionSplit != nil {
		n++
		if err := validateEmissionSplit(*c.EmissionSplit); err != nil {
			return err
		}
	}
	if c.DevelopmentSpend != nil {
		n++
		if err := validateSpend(*c.DevelopmentSpend); err != nil {
			return err
		}
	}
	if c.PowerBounds != nil {
		n++
		if err := validatePowerBounds(*c.PowerBounds); err != nil {
			return err
		}
	}
	if c.RelayReporters != nil {
		n++
		if err := validateReporters(*c.RelayReporters); err != nil {
			return err
		}
	}
	if c.AllowList != nil {
		n++
		if err := validateAllowList(*c.AllowList); err != nil {
			return err
		}
	}
	if n != 1 {
		return fmt.Errorf("proposal content must set exactly one action, got %d", n)
	}
	return nil
}

// Kind reports which tier the content needs.
func (c ProposalContent) Kind() Kind {
	if c.ParameterChange != nil {
		return KindParameter
	}
	if c.SoftwareUpgrade != nil || c.EmissionSplit != nil || c.DevelopmentSpend != nil || c.PowerBounds != nil || c.RelayReporters != nil || c.AllowList != nil {
		return KindStructural
	}
	return KindNone
}

// Timelock is the delay after the proposal passes. Spends are 7 days,
// parameters 14, every other structural action 60. There is no shorter value.
func (c ProposalContent) Timelock() time.Duration {
	switch {
	case c.ParameterChange != nil:
		return ParameterTimelock
	case c.DevelopmentSpend != nil:
		return SpendTimelock
	default:
		return UpgradeTimelock
	}
}

func validateParameterChange(ch ParameterChange) error {
	probe := DefaultParams()
	probe.TokenQuorum = ch.TokenQuorum
	probe.TokenPassThreshold = ch.TokenPassThreshold
	probe.VotingPeriodSeconds = ch.VotingPeriodSeconds
	probe.HouseBond = ch.HouseBond
	probe.MaxEligiblePerPrefix16 = ch.MaxEligiblePerPrefix16
	probe.MaxEligiblePerAsn = ch.MaxEligiblePerAsn
	if err := probe.Validate(); err != nil {
		return fmt.Errorf("parameter change: %w", err)
	}
	return nil
}

func validateUpgrade(u SoftwareUpgrade) error {
	if !upgradeNamePattern.MatchString(u.Name) {
		return fmt.Errorf("software upgrade name %q is not a 1-64 character plan name", u.Name)
	}
	if u.Height <= 0 {
		return fmt.Errorf("software upgrade height must be positive, got %d", u.Height)
	}
	return nil
}

func validateEmissionSplit(s EmissionSplitChange) error {
	sum := s.ValidatorPercent + s.StoragePercent + s.RelayPercent + s.DevelopmentPercent
	if sum != 100 {
		return fmt.Errorf("emission split must sum to 100, got %d", sum)
	}
	checks := []struct {
		name string
		got  uint32
		base uint32
	}{
		{"validator_percent", s.ValidatorPercent, CanonicalValidatorPercent},
		{"storage_percent", s.StoragePercent, CanonicalStoragePercent},
		{"relay_percent", s.RelayPercent, CanonicalRelayPercent},
		{"development_percent", s.DevelopmentPercent, CanonicalDevelopmentPercent},
	}
	for _, c := range checks {
		low := int(c.base) - SplitBoundPoints
		if low < 0 {
			low = 0
		}
		high := int(c.base) + SplitBoundPoints
		if int(c.got) < low || int(c.got) > high {
			return fmt.Errorf("%s must stay within %d points of %d, got %d", c.name, SplitBoundPoints, c.base, c.got)
		}
	}
	return nil
}

func validateSpend(s DevelopmentSpend) error {
	if _, err := sdk.AccAddressFromBech32(s.Recipient); err != nil {
		return fmt.Errorf("development spend recipient: %w", err)
	}
	if s.Amount.IsNil() || !s.Amount.IsPositive() {
		return fmt.Errorf("development spend amount must be positive, got %s", s.Amount)
	}
	if s.Epoch == 0 {
		return fmt.Errorf("development spend epoch must be a closed epoch, got 0")
	}
	return nil
}

func validatePowerBounds(p PowerBoundsChange) error {
	unset := p.MMax.IsNil() || p.MMax.IsZero()
	if unset && !p.ActivateM {
		return fmt.Errorf("power bounds change must activate M or set m_max")
	}
	if !unset && (p.MMax.LT(mMinDec) || p.MMax.GT(mMaxDec)) {
		return fmt.Errorf("m_max must be in [%s, %s], got %s", MMin, MMax, p.MMax)
	}
	return nil
}

func validateReporters(r RelayReporterChange) error {
	if len(r.Add) == 0 && len(r.Remove) == 0 {
		return fmt.Errorf("relay reporter change is empty")
	}
	seen := map[string]string{}
	for _, addr := range r.Add {
		if err := markAddress(seen, addr, "add"); err != nil {
			return fmt.Errorf("relay reporter: %w", err)
		}
	}
	for _, addr := range r.Remove {
		if err := markAddress(seen, addr, "remove"); err != nil {
			return fmt.Errorf("relay reporter: %w", err)
		}
	}
	return nil
}

func markAddress(seen map[string]string, addr, list string) error {
	if _, err := sdk.AccAddressFromBech32(addr); err != nil {
		return fmt.Errorf("%s address %q: %w", list, addr, err)
	}
	if prev, ok := seen[addr]; ok {
		return fmt.Errorf("address %s is listed twice (%s and %s)", addr, prev, list)
	}
	seen[addr] = list
	return nil
}

func validateAllowList(a AllowListChange) error {
	if len(a.CodeUploadAdd)+len(a.CodeUploadRemove)+len(a.AdapterAdd)+len(a.AdapterRemove) == 0 {
		return fmt.Errorf("allow-list change is empty")
	}
	if err := markPattern(a.CodeUploadAdd, a.CodeUploadRemove, codeHashPattern, "code upload"); err != nil {
		return err
	}
	return markPattern(a.AdapterAdd, a.AdapterRemove, adapterPattern, "adapter")
}

func markPattern(add, remove []string, pattern *regexp.Regexp, label string) error {
	seen := map[string]string{}
	for _, item := range add {
		if err := markItem(seen, item, "add", pattern, label); err != nil {
			return err
		}
	}
	for _, item := range remove {
		if err := markItem(seen, item, "remove", pattern, label); err != nil {
			return err
		}
	}
	return nil
}

func markItem(seen map[string]string, item, list string, pattern *regexp.Regexp, label string) error {
	if !pattern.MatchString(item) {
		return fmt.Errorf("%s entry %q is not allowed", label, item)
	}
	if prev, ok := seen[item]; ok {
		return fmt.Errorf("%s entry %s is listed twice (%s and %s)", label, item, prev, list)
	}
	seen[item] = list
	return nil
}

// ValidVoteOption reports whether option is yes, no or abstain.
func ValidVoteOption(o VoteOption) bool {
	return o == VoteOption_YES || o == VoteOption_NO || o == VoteOption_ABSTAIN
}

// IntOrZero returns v, or zero when v was never set.
func IntOrZero(v math.Int) math.Int {
	if v.IsNil() {
		return math.ZeroInt()
	}
	return v
}

// DecOrZero returns v, or zero when v was never set.
func DecOrZero(v math.LegacyDec) math.LegacyDec {
	if v.IsNil() {
		return math.LegacyZeroDec()
	}
	return v
}
