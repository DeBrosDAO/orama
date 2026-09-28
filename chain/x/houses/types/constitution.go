package types

import "time"

// Coded rules (plans/open-network.md D17, track-c-chain.md C5). None of these
// is a governance message field. Parameter-tier changes cannot move them.
const (
	// DelegatedVoteCapPercent is the maximum share of bonded stake one
	// validator's inherited (non-direct) votes can cast. Direct votes are not
	// capped.
	DelegatedVoteCapPercent int64 = 3

	// MinServiceDays is the proven-service age an operator needs, on top of a
	// locked house bond, to be eligible. The operator keeper reports days that
	// already clear its minimum volume.
	MinServiceDays uint64 = 90

	// VetoPercent is the share of the eligible operator house that vetoes a
	// parameter proposal inside the veto window.
	VetoPercent int64 = 30

	// MinHouseSize is the eligible-operator count both tiers require.
	MinHouseSize = 21

	// MinDistinctPrefix16 and MinDistinctASN are the diversity the structural
	// tier and development spends require, counted on the eligible set after
	// the per-network caps.
	MinDistinctPrefix16 = 7
	MinDistinctASN      = 5

	// MaxActiveProposals bounds proposals in voting, veto or timelock so a
	// block does not walk an unbounded active set.
	MaxActiveProposals = 64

	// Canonical emission shares. A structural split may move each by at most
	// SplitBoundPoints and must still sum to 100. The halving schedule and the
	// tail are not shares and cannot be proposed.
	CanonicalValidatorPercent   uint32 = 60
	CanonicalStoragePercent     uint32 = 25
	CanonicalRelayPercent       uint32 = 10
	CanonicalDevelopmentPercent uint32 = 5
	SplitBoundPoints                   = 10
)

const (
	// ParameterTimelock, UpgradeTimelock and SpendTimelock are the only
	// execution delays. There is no expedited delay.
	ParameterTimelock = 14 * 24 * time.Hour
	UpgradeTimelock   = 60 * 24 * time.Hour
	SpendTimelock     = 7 * 24 * time.Hour
	VetoWindow        = 7 * 24 * time.Hour
)

// M bounds match the coded useful-work multiplier range. Activation is one-way.
const (
	MMin = "0.75"
	MMax = "1.25"
)
