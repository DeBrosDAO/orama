package types

import (
	"context"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// BankKeeper is the subset of x/bank x/nodes uses to escrow role bonds in the
// nodes module account and to burn a slash. x/nodes never mints.
type BankKeeper interface {
	SendCoinsFromAccountToModule(ctx context.Context, senderAddr sdk.AccAddress, recipientModule string, amt sdk.Coins) error
	SendCoinsFromModuleToAccount(ctx context.Context, senderModule string, recipientAddr sdk.AccAddress, amt sdk.Coins) error
	BurnCoins(ctx context.Context, moduleName string, amt sdk.Coins) error
	GetBalance(ctx context.Context, addr sdk.AccAddress, denom string) sdk.Coin
}

// DepositKeeper is the subset of x/fees that locks and releases a state
// deposit (plans/open-network/track-c-chain.md C2). Node and cluster writes
// call it; the 99%/1% refund split stays inside x/fees.
type DepositKeeper interface {
	LockDeposit(ctx context.Context, owner sdk.AccAddress, id string, amount math.Int) error
	ReleaseDeposit(ctx context.Context, id string) (refund, burn math.Int, err error)
}

// EarningsKeeper is the subset of x/fees that spends an operator's earnings
// on the two things x/nodes lets earnings do (plans/open-network/track-c-chain.md C2):
//
//   - FundFeeBalance funds an operator's own node's hot key with a fee-only
//     balance (item 5). The balance can pay base fees and is not earnings. It
//     fails when from holds less than amount.
//   - FundBondFromEarnings tops the operator's bank balance up from its own
//     earnings so a role bond can be escrowed (item 3). It moves nothing when
//     the bank balance already covers needed, or when earnings cannot cover
//     the shortfall. It must be called from a message handler, where BaseApp
//     discards the top-up if the message fails.
type EarningsKeeper interface {
	FundFeeBalance(ctx context.Context, from, to sdk.AccAddress, amount math.Int) error
	FundBondFromEarnings(ctx context.Context, addr sdk.AccAddress, denom string, needed math.Int) error
}
