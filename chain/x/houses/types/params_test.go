package types

import (
	"testing"
	"time"

	"cosmossdk.io/math"

	"github.com/DeBrosOfficial/network/chain/app/params"
)

func day(n int64) int64 { return n * int64(24*time.Hour/time.Second) }

func TestParamsValidate_bounds(t *testing.T) {
	oramaToNorama := func(n int64) math.Int { return math.NewInt(n).MulRaw(params.NoramaPerOrama) }
	dec := math.LegacyMustNewDecFromStr

	tests := []struct {
		name   string
		mutate func(*Params)
		ok     bool
	}{
		{"defaults", func(*Params) {}, true},

		{"token_quorum at min", func(p *Params) { p.TokenQuorum = dec("0.334") }, true},
		{"token_quorum below min", func(p *Params) { p.TokenQuorum = dec("0.333") }, false},
		{"token_quorum at max", func(p *Params) { p.TokenQuorum = dec("0.667") }, true},
		{"token_quorum above max", func(p *Params) { p.TokenQuorum = dec("0.668") }, false},

		{"token_pass_threshold at min", func(p *Params) { p.TokenPassThreshold = dec("0.5") }, true},
		{"token_pass_threshold below min", func(p *Params) { p.TokenPassThreshold = dec("0.499") }, false},
		{"token_pass_threshold at max", func(p *Params) { p.TokenPassThreshold = dec("0.667") }, true},
		{"token_pass_threshold above max", func(p *Params) { p.TokenPassThreshold = dec("0.668") }, false},

		{"voting_period at min", func(p *Params) { p.VotingPeriodSeconds = day(1) }, true},
		{"voting_period below min", func(p *Params) { p.VotingPeriodSeconds = day(1) - 1 }, false},
		{"voting_period at max", func(p *Params) { p.VotingPeriodSeconds = day(28) }, true},
		{"voting_period above max", func(p *Params) { p.VotingPeriodSeconds = day(28) + 1 }, false},

		{"house_bond at min", func(p *Params) { p.HouseBond = oramaToNorama(1) }, true},
		{"house_bond below min", func(p *Params) { p.HouseBond = oramaToNorama(1).SubRaw(1) }, false},
		{"house_bond at max", func(p *Params) { p.HouseBond = oramaToNorama(1_000_000) }, true},
		{"house_bond above max", func(p *Params) { p.HouseBond = oramaToNorama(1_000_000).AddRaw(1) }, false},

		{"prefix16 cap at min", func(p *Params) { p.MaxEligiblePerPrefix16 = 1 }, true},
		{"prefix16 cap below min", func(p *Params) { p.MaxEligiblePerPrefix16 = 0 }, false},
		{"prefix16 cap at max", func(p *Params) { p.MaxEligiblePerPrefix16 = 21 }, true},
		{"prefix16 cap above max", func(p *Params) { p.MaxEligiblePerPrefix16 = 22 }, false},

		{"asn cap at min", func(p *Params) { p.MaxEligiblePerAsn = 1 }, true},
		{"asn cap below min", func(p *Params) { p.MaxEligiblePerAsn = 0 }, false},
		{"asn cap at max", func(p *Params) { p.MaxEligiblePerAsn = 21 }, true},
		{"asn cap above max", func(p *Params) { p.MaxEligiblePerAsn = 22 }, false},

		{"min_house_size at min", func(p *Params) { p.MinHouseSize = 21 }, true},
		{"min_house_size below min", func(p *Params) { p.MinHouseSize = 20 }, false},
		{"min_house_size zero", func(p *Params) { p.MinHouseSize = 0 }, false},
		{"min_house_size at max", func(p *Params) { p.MinHouseSize = 101 }, true},
		{"min_house_size above max", func(p *Params) { p.MinHouseSize = 102 }, false},

		{"veto_window at min", func(p *Params) { p.VetoWindowSeconds = day(7) }, true},
		{"veto_window below min", func(p *Params) { p.VetoWindowSeconds = day(7) - 1 }, false},
		{"veto_window at max", func(p *Params) { p.VetoWindowSeconds = day(28) }, true},
		{"veto_window above max", func(p *Params) { p.VetoWindowSeconds = day(28) + 1 }, false},

		{"parameter_timelock at min", func(p *Params) { p.ParameterTimelockSeconds = day(14) }, true},
		{"parameter_timelock below min", func(p *Params) { p.ParameterTimelockSeconds = day(14) - 1 }, false},
		{"parameter_timelock at max", func(p *Params) { p.ParameterTimelockSeconds = day(60) }, true},
		{"parameter_timelock above max", func(p *Params) { p.ParameterTimelockSeconds = day(60) + 1 }, false},

		{"upgrade_timelock at min", func(p *Params) { p.UpgradeTimelockSeconds = day(60) }, true},
		{"upgrade_timelock below min", func(p *Params) { p.UpgradeTimelockSeconds = day(60) - 1 }, false},
		{"upgrade_timelock at max", func(p *Params) { p.UpgradeTimelockSeconds = day(180) }, true},
		{"upgrade_timelock above max", func(p *Params) { p.UpgradeTimelockSeconds = day(180) + 1 }, false},

		{"spend_timelock at min", func(p *Params) { p.SpendTimelockSeconds = day(7) }, true},
		{"spend_timelock below min", func(p *Params) { p.SpendTimelockSeconds = day(7) - 1 }, false},
		{"spend_timelock zero (expedited)", func(p *Params) { p.SpendTimelockSeconds = 0 }, false},
		{"spend_timelock at max", func(p *Params) { p.SpendTimelockSeconds = day(30) }, true},
		{"spend_timelock above max", func(p *Params) { p.SpendTimelockSeconds = day(30) + 1 }, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := DefaultParams()
			tc.mutate(&p)
			err := p.Validate()
			if tc.ok && err != nil {
				t.Fatalf("want valid, got %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatal("want error, got nil")
			}
		})
	}
}

func TestParamsTimelockFor_usesGenesisValues(t *testing.T) {
	p := DefaultParams()
	p.ParameterTimelockSeconds = day(20)
	p.UpgradeTimelockSeconds = day(70)
	p.SpendTimelockSeconds = day(9)

	cases := []struct {
		name    string
		content ProposalContent
		want    time.Duration
	}{
		{"parameter", ProposalContent{ParameterChange: &ParameterChange{}}, 20 * 24 * time.Hour},
		{"spend", ProposalContent{DevelopmentSpend: &DevelopmentSpend{}}, 9 * 24 * time.Hour},
		{"upgrade", ProposalContent{SoftwareUpgrade: &SoftwareUpgrade{}}, 70 * 24 * time.Hour},
		{"allow list", ProposalContent{AllowList: &AllowListChange{}}, 70 * 24 * time.Hour},
	}
	for _, tc := range cases {
		if got := p.TimelockFor(tc.content); got != tc.want {
			t.Fatalf("%s: got %s want %s", tc.name, got, tc.want)
		}
	}
}

func TestDefaultParams_planValues(t *testing.T) {
	p := DefaultParams()
	if p.MinHouseSize != 21 || p.VetoWindow() != 7*24*time.Hour {
		t.Fatalf("house size %d veto %s", p.MinHouseSize, p.VetoWindow())
	}
	if p.ParameterTimelockSeconds != day(14) || p.UpgradeTimelockSeconds != day(60) || p.SpendTimelockSeconds != day(7) {
		t.Fatalf("timelocks %d/%d/%d", p.ParameterTimelockSeconds, p.UpgradeTimelockSeconds, p.SpendTimelockSeconds)
	}
}
