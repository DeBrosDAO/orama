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

	// FundBondFromEarnings tops addr's bank balance up from its own earnings so a message can pull
	// needed from it (C2 item 4: earnings fund the signer's own deals). It must be called from a
	// message handler, after the handler's own checks and before it pulls funds: BaseApp discards
	// the message's state when it fails, and the top-up goes with it.
	FundBondFromEarnings(ctx context.Context, addr sdk.AccAddress, denom string, needed math.Int) error
}

// DepositKeeper matches x/fees Keeper.LockDeposit, TopUpDeposit, DepositAmount and
// ReleaseDeposit. x/storage uses it for the probation record deposit (taken progressively from
// first earnings, recovered when the node graduates). It does not import x/fees. Escrow does not
// go through this interface: ReleaseDeposit burns 1%, and escrow must be conserved in full.
type DepositKeeper interface {
	LockDeposit(ctx context.Context, owner sdk.AccAddress, id string, amount math.Int) error
	TopUpDeposit(ctx context.Context, id string, extra math.Int) error
	// DepositAmount returns the amount locked under id, and false when no deposit is open.
	DepositAmount(ctx context.Context, id string) (amount math.Int, found bool, err error)
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
//
// IsActive means the node's STORAGE role is bonded at its minimum and the node is not jailed,
// retired or tombstoned. Network16 is "" when the node has no literal-IP endpoint and ASN is 0
// when the operator declared none; neither is verified on chain (docs/CHAIN.md, "Node network
// identity").
//
// IsProbation means the node is a fee-free probation registration: it has the STORAGE role, no
// STORAGE bond, and is not jailed, retired or tombstoned. Such a node is not IsActive; x/storage
// tracks it with Probation set, gives it only protocol-deal slots under the probation caps, and
// recovers its record deposit when it graduates or is untracked.
//
// TakeStorageChanges drains the ids of STORAGE-role nodes written since the previous call, in id
// order. MarkStorageChanged queues one back. x/storage reconciles its own node set from these in
// BeginBlock (see Keeper.syncNodes).
type NodeView interface {
	IsActive(ctx context.Context, nodeID string) (bool, error)
	IsProbation(ctx context.Context, nodeID string) (bool, error)
	TakeStorageChanges(ctx context.Context) ([]string, error)
	MarkStorageChanged(ctx context.Context, nodeID string) error
	HotKey(ctx context.Context, nodeID string) (sdk.AccAddress, error)
	Operator(ctx context.Context, nodeID string) (string, error)
	Network16(ctx context.Context, nodeID string) (string, error)
	ASN(ctx context.Context, nodeID string) (uint32, error)
	DeclaredCapacity(ctx context.Context, nodeID string) (uint64, error)
	Slash(ctx context.Context, nodeID string, amount math.Int) error
	Jail(ctx context.Context, nodeID string) error
}
