package types

import (
	"fmt"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// DefaultGenesisState is a chain on which nobody governs yet.
func DefaultGenesisState() *GenesisState {
	return &GenesisState{
		Params:         DefaultParams(),
		Proposals:      []Proposal{},
		TokenVotes:     []Vote{},
		OperatorVotes:  []Vote{},
		Bonds:          []HouseBond{},
		Equivocations:  []Equivocation{},
		Enacted:        Enacted{MMax: math.LegacyZeroDec()},
		NextProposalId: 1,
	}
}

// Validate checks genesis state.
func (gs GenesisState) Validate() error {
	if err := gs.Params.Validate(); err != nil {
		return err
	}
	if gs.NextProposalId == 0 {
		return fmt.Errorf("next_proposal_id must be positive")
	}
	if err := validateEnacted(gs.Enacted); err != nil {
		return err
	}

	proposals := make(map[uint64]Proposal, len(gs.Proposals))
	for _, p := range gs.Proposals {
		if p.Id == 0 || p.Id >= gs.NextProposalId {
			return fmt.Errorf("proposal id %d must be in [1, %d)", p.Id, gs.NextProposalId)
		}
		if _, ok := proposals[p.Id]; ok {
			return fmt.Errorf("duplicate proposal id %d", p.Id)
		}
		if _, err := sdk.AccAddressFromBech32(p.Proposer); err != nil {
			return fmt.Errorf("proposal %d proposer: %w", p.Id, err)
		}
		if err := p.Content.ValidateBasic(); err != nil {
			return fmt.Errorf("proposal %d: %w", p.Id, err)
		}
		if !knownStatus(p.Status) {
			return fmt.Errorf("proposal %d has unknown status %d", p.Id, p.Status)
		}
		if p.SubmitUnixNano <= 0 || p.VotingEndUnixNano <= p.SubmitUnixNano {
			return fmt.Errorf("proposal %d voting window is not positive", p.Id)
		}
		if p.TokenYes.IsNil() || p.TokenNo.IsNil() || p.TokenAbstain.IsNil() || p.TokenYes.IsNegative() || p.TokenNo.IsNegative() || p.TokenAbstain.IsNegative() {
			return fmt.Errorf("proposal %d tallies must be non-negative", p.Id)
		}
		if p.Status == ProposalStatus_VETO_WINDOW && p.VetoEndUnixNano <= p.VotingEndUnixNano {
			return fmt.Errorf("proposal %d veto window does not follow voting", p.Id)
		}
		if p.Status == ProposalStatus_TIMELOCK && p.TimelockEndUnixNano <= p.VotingEndUnixNano {
			return fmt.Errorf("proposal %d timelock does not follow voting", p.Id)
		}
		proposals[p.Id] = p
	}
	if err := validateVotes("token", gs.TokenVotes, proposals); err != nil {
		return err
	}
	if err := validateVotes("operator", gs.OperatorVotes, proposals); err != nil {
		return err
	}

	seenBonds := map[string]bool{}
	for _, b := range gs.Bonds {
		if _, err := sdk.AccAddressFromBech32(b.Address); err != nil {
			return fmt.Errorf("house bond address: %w", err)
		}
		if seenBonds[b.Address] {
			return fmt.Errorf("duplicate house bond %s", b.Address)
		}
		seenBonds[b.Address] = true
		if b.Amount.IsNil() || !b.Amount.IsPositive() {
			return fmt.Errorf("house bond %s amount must be positive", b.Address)
		}
		if b.LockedAtHeight < 0 {
			return fmt.Errorf("house bond %s locked_at_height must be non-negative", b.Address)
		}
	}

	seenEq := map[string]bool{}
	for _, e := range gs.Equivocations {
		if _, ok := proposals[e.ProposalId]; !ok {
			return fmt.Errorf("equivocation references unknown proposal %d", e.ProposalId)
		}
		if _, err := sdk.AccAddressFromBech32(e.Operator); err != nil {
			return fmt.Errorf("equivocation operator: %w", err)
		}
		key := fmt.Sprintf("%d/%s", e.ProposalId, e.Operator)
		if seenEq[key] {
			return fmt.Errorf("duplicate equivocation %s", key)
		}
		seenEq[key] = true
	}
	return nil
}

func knownStatus(s ProposalStatus) bool {
	switch s {
	case ProposalStatus_VOTING, ProposalStatus_VETO_WINDOW, ProposalStatus_TIMELOCK, ProposalStatus_EXECUTED, ProposalStatus_REJECTED, ProposalStatus_FAILED:
		return true
	default:
		return false
	}
}

func validateVotes(label string, votes []Vote, proposals map[uint64]Proposal) error {
	seen := map[string]bool{}
	for _, v := range votes {
		if _, ok := proposals[v.ProposalId]; !ok {
			return fmt.Errorf("%s vote references unknown proposal %d", label, v.ProposalId)
		}
		if _, err := sdk.AccAddressFromBech32(v.Voter); err != nil {
			return fmt.Errorf("%s vote voter: %w", label, err)
		}
		if !ValidVoteOption(v.Option) {
			return fmt.Errorf("%s vote on proposal %d has an empty option", label, v.ProposalId)
		}
		key := fmt.Sprintf("%d/%s", v.ProposalId, v.Voter)
		if seen[key] {
			return fmt.Errorf("duplicate %s vote %s", label, key)
		}
		seen[key] = true
	}
	return nil
}

func validateEnacted(e Enacted) error {
	if e.MMax.IsNil() {
		return fmt.Errorf("enacted m_max must be set, zero if unused")
	}
	if !e.MMax.IsZero() && (e.MMax.LT(mMinDec) || e.MMax.GT(mMaxDec)) {
		return fmt.Errorf("enacted m_max must be 0 or in [%s, %s], got %s", MMin, MMax, e.MMax)
	}
	if e.ScheduledUpgrade != nil {
		if err := validateUpgrade(*e.ScheduledUpgrade); err != nil {
			return fmt.Errorf("enacted upgrade: %w", err)
		}
	}
	if e.EmissionSplit != nil {
		if err := validateEmissionSplit(*e.EmissionSplit); err != nil {
			return fmt.Errorf("enacted split: %w", err)
		}
	}
	if err := uniqueAddresses(e.RelayReporters); err != nil {
		return fmt.Errorf("enacted relay reporters: %w", err)
	}
	if err := uniquePattern(e.CodeUploadAllow, codeHashPattern, "code upload"); err != nil {
		return fmt.Errorf("enacted allow-list: %w", err)
	}
	if err := uniquePattern(e.AdapterAllow, adapterPattern, "adapter"); err != nil {
		return fmt.Errorf("enacted allow-list: %w", err)
	}
	return nil
}

func uniqueAddresses(addrs []string) error {
	seen := map[string]bool{}
	for _, addr := range addrs {
		if _, err := sdk.AccAddressFromBech32(addr); err != nil {
			return err
		}
		if seen[addr] {
			return fmt.Errorf("duplicate address %s", addr)
		}
		seen[addr] = true
	}
	return nil
}

func uniquePattern(items []string, pattern interface{ MatchString(string) bool }, label string) error {
	seen := map[string]bool{}
	for _, item := range items {
		if !pattern.MatchString(item) {
			return fmt.Errorf("%s entry %q is not allowed", label, item)
		}
		if seen[item] {
			return fmt.Errorf("duplicate %s entry %s", label, item)
		}
		seen[item] = true
	}
	return nil
}
