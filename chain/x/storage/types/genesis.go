package types

import (
	"fmt"

	"cosmossdk.io/math"
)

// DefaultGenesisState returns an empty x/storage genesis. LastEpoch 0 means
// InitGenesis adopts the emission module's current epoch.
func DefaultGenesisState() *GenesisState {
	return &GenesisState{
		Params:         DefaultParams(),
		Deals:          []Deal{},
		Slots:          []Slot{},
		Authorizations: []DealAuthorization{},
		Nodes:          []NodeState{},
		Settlements:    []Settlement{},
		Challenges:     []GenesisChallenge{},
		Rechallenges:   []Rechallenge{},
		NextDealId:     1,
		ArchiveFund:    math.ZeroInt(),
		EpochMints:     []EpochMint{},
		OperatorMints:  []OperatorMint{},
		ReleaseCounts:  []ReleaseCount{},
		Reserved:       []Reserved{},
		FailureCounts:  []FailureCount{},
	}
}

// NormalizeInt returns v, or zero when v was never set.
func NormalizeInt(v math.Int) math.Int {
	if v.IsNil() {
		return math.ZeroInt()
	}
	return v
}

// Validate checks genesis for internal consistency.
func (gs GenesisState) Validate() error {
	if err := gs.Params.Validate(); err != nil {
		return err
	}
	if gs.NextDealId == 0 {
		return fmt.Errorf("next_deal_id must be positive")
	}
	if gs.ArchiveFund.IsNil() || gs.ArchiveFund.IsNegative() {
		return fmt.Errorf("archive_fund must be non-negative")
	}
	if gs.QueueHead > gs.QueueTail {
		return fmt.Errorf("queue head %d is past tail %d", gs.QueueHead, gs.QueueTail)
	}
	seenDeals := map[uint64]bool{}
	for _, d := range gs.Deals {
		if d.Id == 0 || seenDeals[d.Id] {
			return fmt.Errorf("duplicate or zero deal id %d", d.Id)
		}
		seenDeals[d.Id] = true
		if d.Id >= gs.NextDealId {
			return fmt.Errorf("deal id %d is not below next_deal_id %d", d.Id, gs.NextDealId)
		}
		if d.Escrow.IsNil() || d.Escrow.IsNegative() {
			return fmt.Errorf("deal %d escrow must be non-negative", d.Id)
		}
		if d.PricePerEpoch.IsNil() || !d.PricePerEpoch.IsPositive() {
			return fmt.Errorf("deal %d price must be positive", d.Id)
		}
	}
	seenFailures := map[[2]string]bool{}
	for _, f := range gs.FailureCounts {
		key := [2]string{f.NodeId, f.Kind}
		if f.NodeId == "" || f.Kind == "" || f.Consecutive == 0 || seenFailures[key] {
			return fmt.Errorf("failure count %q/%q must be unique, named, and non-zero", f.NodeId, f.Kind)
		}
		seenFailures[key] = true
	}
	seenSlots := map[[2]uint64]bool{}
	for _, s := range gs.Slots {
		key := [2]uint64{s.DealId, uint64(s.Index)}
		if seenSlots[key] {
			return fmt.Errorf("duplicate slot %d/%d", s.DealId, s.Index)
		}
		seenSlots[key] = true
		if !seenDeals[s.DealId] {
			return fmt.Errorf("slot %d/%d has no deal", s.DealId, s.Index)
		}
	}
	if uint64(len(gs.Settlements)) != gs.QueueTail-gs.QueueHead {
		return fmt.Errorf("settlement entries %d do not match queue span %d", len(gs.Settlements), gs.QueueTail-gs.QueueHead)
	}
	seenSeq := map[uint64]bool{}
	for _, s := range gs.Settlements {
		if s.Seq < gs.QueueHead || s.Seq >= gs.QueueTail || seenSeq[s.Seq] {
			return fmt.Errorf("settlement seq %d is outside [%d, %d)", s.Seq, gs.QueueHead, gs.QueueTail)
		}
		seenSeq[s.Seq] = true
		if s.EscrowPay.IsNil() || s.EscrowPay.IsNegative() || s.MintPay.IsNil() || s.MintPay.IsNegative() || s.ArchiveTopUp.IsNil() || s.ArchiveTopUp.IsNegative() {
			return fmt.Errorf("settlement %d amounts must be non-negative", s.Seq)
		}
	}
	for _, m := range gs.EpochMints {
		if m.Minted.IsNil() || m.Minted.IsNegative() {
			return fmt.Errorf("epoch %d mint must be non-negative", m.Epoch)
		}
	}
	return nil
}
