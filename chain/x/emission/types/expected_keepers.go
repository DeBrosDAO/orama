package types

import (
	"context"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	powertypes "github.com/DeBrosOfficial/network/chain/x/power/types"
)

// BankKeeper is the subset of x/bank's keeper that x/emission needs: minting the validator share
// (which x/power's DistributeEpochRewards then pulls out of x/emission's own module account - see
// PowerKeeper) and reading total supply for the invariant check. x/emission sends coins to a user account only for
// a test-network faucet drip.
type BankKeeper interface {
	MintCoins(ctx context.Context, moduleName string, amt sdk.Coins) error
	// SendCoinsFromModuleToModule moves a storage or relay service mint from
	// x/emission, the only norama minter, to the module that pays it out.
	SendCoinsFromModuleToModule(ctx context.Context, senderModule, recipientModule string, amt sdk.Coins) error
	// SendCoinsFromModuleToAccount pays a faucet drip from x/emission's module account to the
	// recipient, creating the account if it does not exist.
	SendCoinsFromModuleToAccount(ctx context.Context, senderModule string, recipientAddr sdk.AccAddress, amt sdk.Coins) error
	// BlockedAddr reports whether addr (a module account) may not receive a direct send.
	BlockedAddr(addr sdk.AccAddress) bool
	GetSupply(ctx context.Context, denom string) sdk.Coin
	// GetBalance is used only by the devnet-only bootstrap-stake premine gate (Keeper.InitGenesis):
	// it checks that genesis supply sits entirely in the staking bonded pool, never idle elsewhere.
	GetBalance(ctx context.Context, addr sdk.AccAddress, denom string) sdk.Coin
}

// PowerKeeper is the subset of x/power's keeper x/emission needs to pay each closed epoch's
// validator/delegator share on capped power P_i instead of forwarding it to the fee collector for
// stock x/distribution to pay out on raw stake
// (plans/open-network/track-c-chain.md C3: "It does not use the stock distribution module ...
// Delegators get their share pro rata inside each validator"; C4: "Rewards are paid on actual
// power P_i"). Implemented by x/power/keeper.Keeper.
type PowerKeeper interface {
	// DistributeEpochRewards pulls totalMint out of sourceModule's own account (x/emission's) and
	// pays it out; emissionKeeper is x/emission's own keeper, passed back in so x/power can read
	// the current epoch number without an import cycle (see power/types.EmissionKeeper).
	DistributeEpochRewards(ctx sdk.Context, emissionKeeper powertypes.EmissionKeeper, sourceModule string, totalMint math.Int) (math.Int, error)
}
