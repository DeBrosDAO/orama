package types

import (
	"context"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// FeesKeeper is the subset of x/fees used to lock a tree's state deposit.
// x/cnft does not release it and does not mint.
type FeesKeeper interface {
	LockDeposit(ctx context.Context, owner sdk.AccAddress, id string, amount math.Int) error
}

// EarningsKeeper is the earnings credit used by sale proceeds. x/cnft transfers
// are not sales and must not call it; x/market credits royalties itself.
type EarningsKeeper interface {
	CreditEarnings(ctx context.Context, senderModule string, addr sdk.AccAddress, amt sdk.Coin) error
}
