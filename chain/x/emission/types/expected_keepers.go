package types

import (
	"context"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// BankKeeper is the subset of x/bank's keeper that x/emission needs: minting the validator share
// and reading total supply for the invariant check. x/emission never sends coins to any user
// account directly; it only mints to its own module account and forwards to the fee collector, so
// x/distribution's existing BeginBlocker pays validators and delegators on capped power
// (plans/open-network/track-c-chain.md C3).
type BankKeeper interface {
	MintCoins(ctx context.Context, moduleName string, amt sdk.Coins) error
	SendCoinsFromModuleToModule(ctx context.Context, senderModule, recipientModule string, amt sdk.Coins) error
	GetSupply(ctx context.Context, denom string) sdk.Coin
	// GetBalance is used only by the devnet-only bootstrap-stake premine gate (Keeper.InitGenesis):
	// it checks that genesis supply sits entirely in the staking bonded pool, never idle elsewhere.
	GetBalance(ctx context.Context, addr sdk.AccAddress, denom string) sdk.Coin
}
