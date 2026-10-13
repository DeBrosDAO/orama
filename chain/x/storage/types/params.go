package types

import (
	"fmt"

	"cosmossdk.io/math"
)

const (
	// DefaultMinDealBytes is one leaf. Smaller user pieces are rejected.
	DefaultMinDealBytes uint64 = 1024
	// DefaultDealFee is the burned per-deal fee, in norama.
	DefaultDealFee int64 = 1_000
	// DefaultMaxDealsPerBlock caps user creations in one block.
	DefaultMaxDealsPerBlock uint64 = 100
	// DefaultKC is the indexed sample size per provider per epoch.
	DefaultKC uint64 = 8
	// DefaultMissThreshold evicts a slot after this many consecutive misses.
	// Slashing starts at two consecutive misses, so the threshold is higher.
	DefaultMissThreshold uint32 = 4
	// DefaultMaxSettlementsPerBlock is the settlement queue's per-block limit.
	DefaultMaxSettlementsPerBlock uint64 = 100
	// DefaultSMinProviders is the distinct-operator count below which s is 0.
	DefaultSMinProviders uint64 = 8
	// DefaultSFullProviders is where the linear ramp reaches the hard-coded s_max.
	DefaultSFullProviders uint64 = 32
	// DefaultAcceptWindowBlocks is the hot-key accept/decline window.
	DefaultAcceptWindowBlocks uint64 = 50
	// DefaultProbationSlots caps protocol-deal slots on one probation node.
	DefaultProbationSlots uint32 = 1
	// DefaultProbationOperatorCap caps protocol-deal slots held by probation
	// nodes of one operator.
	DefaultProbationOperatorCap uint32 = 2
	// DefaultProbationNetworkCap is the same cap for one /16.
	DefaultProbationNetworkCap uint32 = 2
	// DefaultProbationASNCap is the same cap for one ASN.
	DefaultProbationASNCap uint32 = 2
	// DefaultProbationExpiryEpochs jails a probation node that has proved nothing.
	DefaultProbationExpiryEpochs uint64 = 4
	// DefaultProbationDeposit is the record deposit, in norama.
	DefaultProbationDeposit int64 = 1_000
	// DefaultMaxReleasesPerEpoch rate-limits MsgReleaseReplica.
	DefaultMaxReleasesPerEpoch uint64 = 2
	// DefaultProtocolEveryEpochs disables scheduled protocol deals until genesis sets it.
	DefaultProtocolEveryEpochs uint64 = 0
	// DefaultSlashFraction is the fraction of epoch price slashed once two
	// consecutive misses have accrued.
	DefaultSlashFraction = "0.1"
	// DefaultProtocolPieceBytes is the scheduled protocol payload size.
	DefaultProtocolPieceBytes uint64 = 1024
	// DefaultProtocolPrice is the per-replica epoch price of a scheduled protocol deal.
	DefaultProtocolPrice int64 = 1_000
	// DefaultProtocolDurationEpochs is the length of a scheduled protocol deal.
	DefaultProtocolDurationEpochs uint64 = 1

	maxProviderParam uint64 = 1_000_000
)

// DefaultParams returns genesis parameters. s_max is not among them.
func DefaultParams() Params {
	return Params{
		MinDealBytes:           DefaultMinDealBytes,
		DealFee:                math.NewInt(DefaultDealFee),
		MaxDealsPerBlock:       DefaultMaxDealsPerBlock,
		KC:                     DefaultKC,
		MissThreshold:          DefaultMissThreshold,
		MaxSettlementsPerBlock: DefaultMaxSettlementsPerBlock,
		SMinProviders:          DefaultSMinProviders,
		SFullProviders:         DefaultSFullProviders,
		AcceptWindowBlocks:     DefaultAcceptWindowBlocks,
		ProbationSlots:         DefaultProbationSlots,
		ProbationOperatorCap:   DefaultProbationOperatorCap,
		ProbationNetwork16Cap:  DefaultProbationNetworkCap,
		ProbationAsnCap:        DefaultProbationASNCap,
		ProbationExpiryEpochs:  DefaultProbationExpiryEpochs,
		ProbationDeposit:       math.NewInt(DefaultProbationDeposit),
		MaxReleasesPerEpoch:    DefaultMaxReleasesPerEpoch,
		ProtocolEveryEpochs:    DefaultProtocolEveryEpochs,
		SlashFraction:          math.LegacyMustNewDecFromStr(DefaultSlashFraction),
		ProtocolPieceBytes:     DefaultProtocolPieceBytes,
		ProtocolPricePerEpoch:  math.NewInt(DefaultProtocolPrice),
		ProtocolDurationEpochs: DefaultProtocolDurationEpochs,
	}
}

// Validate checks Params. It cannot raise s above the hard-coded maximum,
// because s_max is not a field.
func (p Params) Validate() error {
	if p.MinDealBytes == 0 {
		return fmt.Errorf("min_deal_bytes must be positive")
	}
	if p.DealFee.IsNil() || !p.DealFee.IsPositive() {
		return fmt.Errorf("deal_fee must be a positive integer")
	}
	if p.MaxDealsPerBlock == 0 {
		return fmt.Errorf("max_deals_per_block must be positive")
	}
	if p.MissThreshold < 2 {
		return fmt.Errorf("miss_threshold must be at least 2, got %d", p.MissThreshold)
	}
	if p.MaxSettlementsPerBlock == 0 {
		return fmt.Errorf("max_settlements_per_block must be positive")
	}
	if p.SMinProviders == 0 || p.SMinProviders > maxProviderParam {
		return fmt.Errorf("s_min_providers must be in [1, %d], got %d", maxProviderParam, p.SMinProviders)
	}
	if p.SFullProviders < p.SMinProviders || p.SFullProviders > maxProviderParam {
		return fmt.Errorf("s_full_providers must be in [s_min_providers, %d], got %d", maxProviderParam, p.SFullProviders)
	}
	if p.AcceptWindowBlocks == 0 {
		return fmt.Errorf("accept_window_blocks must be positive")
	}
	if p.ProbationSlots == 0 || p.ProbationOperatorCap == 0 || p.ProbationNetwork16Cap == 0 || p.ProbationAsnCap == 0 {
		return fmt.Errorf("probation caps must be positive")
	}
	if p.ProbationExpiryEpochs == 0 {
		return fmt.Errorf("probation_expiry_epochs must be positive")
	}
	if p.ProbationDeposit.IsNil() || !p.ProbationDeposit.IsPositive() {
		return fmt.Errorf("probation_deposit must be a positive integer")
	}
	if p.MaxReleasesPerEpoch == 0 {
		return fmt.Errorf("max_releases_per_epoch must be positive")
	}
	if p.SlashFraction.IsNil() || !p.SlashFraction.IsPositive() || p.SlashFraction.GT(math.LegacyOneDec()) {
		return fmt.Errorf("slash_fraction must be in (0, 1], got %s", p.SlashFraction)
	}
	if p.ProtocolPieceBytes == 0 {
		return fmt.Errorf("protocol_piece_bytes must be positive")
	}
	if p.ProtocolPricePerEpoch.IsNil() || !p.ProtocolPricePerEpoch.IsPositive() {
		return fmt.Errorf("protocol_price_per_epoch must be a positive integer")
	}
	if p.ProtocolDurationEpochs == 0 || p.ProtocolDurationEpochs > MaxDurationEpochs {
		return fmt.Errorf("protocol_duration_epochs must be in [1, %d]", MaxDurationEpochs)
	}
	return nil
}
