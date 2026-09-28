package types

import (
	"context"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// BondedDelegation is one bonded position. Delegator == Validator is a
// self-bond. Amount is norama.
type BondedDelegation struct {
	Delegator sdk.AccAddress
	Validator sdk.AccAddress
	Amount    math.Int
}

// StakingKeeper supplies the bonded stake the token house tallies. x/houses
// does not import x/staking.
type StakingKeeper interface {
	TotalBondedTokens(ctx context.Context) (math.Int, error)
	Delegations(ctx context.Context) ([]BondedDelegation, error)
}

// PowerKeeper supplies lambda. x/houses does not import x/power. Lambda is
// reached when it is greater than or equal to 1.
type PowerKeeper interface {
	Lambda(ctx context.Context) (math.LegacyDec, error)
}

// OperatorInfo is one operator identity. Prefix16 and ASN are compared as the
// operator keeper normalized them. ServiceDays counts only proven service
// above that keeper's minimum volume.
type OperatorInfo struct {
	Address     sdk.AccAddress
	Prefix16    string
	ASN         uint32
	ServiceDays uint64
}

// OperatorKeeper lists operators. x/houses does not import x/nodes.
type OperatorKeeper interface {
	Operators(ctx context.Context) ([]OperatorInfo, error)
}

// BankKeeper moves house bonds into and out of the houses module account and
// burns a bond on equivocation. x/houses never mints.
type BankKeeper interface {
	SendCoinsFromAccountToModule(ctx context.Context, senderAddr sdk.AccAddress, recipientModule string, amt sdk.Coins) error
	SendCoinsFromModuleToAccount(ctx context.Context, senderModule string, recipientAddr sdk.AccAddress, amt sdk.Coins) error
	BurnCoins(ctx context.Context, moduleName string, amt sdk.Coins) error
	GetBalance(ctx context.Context, addr sdk.AccAddress, denom string) sdk.Coin
}

// EmissionKeeper mints an approved development spend and returns the module
// account the coins were minted into. It must refuse any amount that is not
// within the epoch's remaining development ceiling, and it must not write
// state when it returns an error.
type EmissionKeeper interface {
	MintDevelopmentSpend(ctx context.Context, epoch uint64, amount math.Int) (moduleAccount string, err error)
}

// EarningsKeeper credits a development spend to the recipient's earnings
// account, pulling the coins from the emission module account.
type EarningsKeeper interface {
	CreditEarnings(ctx context.Context, senderModule string, addr sdk.AccAddress, amt sdk.Coin) error
}
