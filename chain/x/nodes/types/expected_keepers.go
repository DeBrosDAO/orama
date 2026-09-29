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

// EarningsKeeper is the subset of x/fees that moves value between two
// earnings accounts without releasing it to a bank balance
// (plans/open-network/track-c-chain.md C2 item 5: an operator funds its own
// node's hot key from its earnings). MoveEarnings fails when from holds less
// than amount.
type EarningsKeeper interface {
	MoveEarnings(ctx context.Context, from, to sdk.AccAddress, amount math.Int) error
}
