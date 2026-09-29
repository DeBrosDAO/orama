package types

import (
	"context"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// BankKeeper is the subset of x/bank x/storage uses. Burns take the per-deal
// fee and the 5% service-payment burn. Subsidy mints go through
// EmissionKeeper.MintStorageService; x/storage cannot mint.
type BankKeeper interface {
	SendCoinsFromAccountToModule(ctx context.Context, senderAddr sdk.AccAddress, recipientModule string, amt sdk.Coins) error
	SendCoinsFromModuleToModule(ctx context.Context, senderModule, recipientModule string, amt sdk.Coins) error
	SendCoinsFromModuleToAccount(ctx context.Context, senderModule string, recipientAddr sdk.AccAddress, amt sdk.Coins) error
	BurnCoins(ctx context.Context, moduleName string, amt sdk.Coins) error
	SpendableCoins(ctx context.Context, addr sdk.AccAddress) sdk.Coins
	GetBalance(ctx context.Context, addr sdk.AccAddress, denom string) sdk.Coin
}

// EarningsKeeper matches x/fees Keeper.CreditEarnings (chain/x/power/types.EarningsKeeper).
// x/storage does not import x/fees. The 90/5/5 service split is applied here, before
// the 90% is credited: x/fees has no service-split of its own.
type EarningsKeeper interface {
	CreditEarnings(ctx context.Context, senderModule string, addr sdk.AccAddress, amt sdk.Coin) error
}

// DepositKeeper matches x/fees Keeper.LockDeposit and ReleaseDeposit. x/storage uses
// it for the probation record deposit (taken from first earnings, recovered when the
// node graduates). It does not import x/fees. Escrow does not go through this
// interface: ReleaseDeposit burns 1%, and escrow must be conserved in full.
type DepositKeeper interface {
	LockDeposit(ctx context.Context, owner sdk.AccAddress, id string, amount math.Int) error
	ReleaseDeposit(ctx context.Context, id string) (refund, burn math.Int, err error)
}

// EmissionKeeper is the storage-ceiling view of x/emission. StorageCeiling matches
// CeilingRecord.StorageCeiling for one closed epoch. CurrentEpoch matches
// x/emission Keeper.CurrentEpoch. x/storage does not import x/emission.
type EmissionKeeper interface {
	CurrentEpoch(ctx context.Context) (uint64, error)
	StorageCeiling(ctx context.Context, epoch uint64) (math.Int, error)
	// MintStorageService mints a subsidy payment into the storage module
	// account against the epoch's storage ceiling. x/emission is the only
	// module that mints norama.
	MintStorageService(ctx context.Context, epoch uint64, amt math.Int) error
}

// NodeView is the storage-relevant view of a node. x/storage does not import x/nodes.
// Operator returns the operator's earnings address (bech32). DeclaredCapacity is bytes.
type NodeView interface {
	IsActive(ctx context.Context, nodeID string) (bool, error)
	HotKey(ctx context.Context, nodeID string) (sdk.AccAddress, error)
	Operator(ctx context.Context, nodeID string) (string, error)
	Network16(ctx context.Context, nodeID string) (string, error)
	ASN(ctx context.Context, nodeID string) (uint32, error)
	DeclaredCapacity(ctx context.Context, nodeID string) (uint64, error)
	Slash(ctx context.Context, nodeID string, amount math.Int) error
	Jail(ctx context.Context, nodeID string) error
}
