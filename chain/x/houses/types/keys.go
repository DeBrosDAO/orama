package types

import "cosmossdk.io/collections"

const (
	// ModuleName is the name of x/houses and of the module account that holds
	// locked house bonds.
	ModuleName = "houses"

	// StoreKey is the store key for x/houses.
	StoreKey = ModuleName
)

var (
	ParamsKey           = collections.NewPrefix(0)
	NextProposalIDKey   = collections.NewPrefix(1)
	ProposalsPrefix     = collections.NewPrefix(2)
	TokenVotesPrefix    = collections.NewPrefix(3)
	OperatorVotesPrefix = collections.NewPrefix(4)
	BondsPrefix         = collections.NewPrefix(5)
	ActivePrefix        = collections.NewPrefix(6)
	EquivocationsPrefix = collections.NewPrefix(7)
	EnactedKey          = collections.NewPrefix(8)
)
