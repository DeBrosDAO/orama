package types

import (
	"context"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// BankKeeper is the subset of x/bank's keeper x/fees needs: moving coins into and out of its two
// module accounts (ModuleName for earnings/base-fee/tip bookkeeping, DepositsModuleName for locked
// state deposits) and burning the base-fee and deposit-burn shares. x/fees never mints.
type BankKeeper interface {
	SendCoinsFromAccountToModule(ctx context.Context, senderAddr sdk.AccAddress, recipientModule string, amt sdk.Coins) error
	SendCoinsFromModuleToModule(ctx context.Context, senderModule, recipientModule string, amt sdk.Coins) error
	SendCoinsFromModuleToAccount(ctx context.Context, senderModule string, recipientAddr sdk.AccAddress, amt sdk.Coins) error
	BurnCoins(ctx context.Context, moduleName string, amt sdk.Coins) error
	SpendableCoins(ctx context.Context, addr sdk.AccAddress) sdk.Coins
}
