package types

import (
	"context"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	feestypes "github.com/DeBrosOfficial/network/chain/x/fees/types"
)

// BankKeeper is the subset of x/bank x/token needs. Every balance lives in
// bank. x/token mints and burns only its own factory denoms, plus the norama
// creation fee and the token-denominated transfer fee.
type BankKeeper interface {
	MintCoins(ctx context.Context, moduleName string, amt sdk.Coins) error
	BurnCoins(ctx context.Context, moduleName string, amt sdk.Coins) error
	SendCoins(ctx context.Context, fromAddr, toAddr sdk.AccAddress, amt sdk.Coins) error
	SendCoinsFromModuleToAccount(ctx context.Context, senderModule string, recipientAddr sdk.AccAddress, amt sdk.Coins) error
	SendCoinsFromAccountToModule(ctx context.Context, senderAddr sdk.AccAddress, recipientModule string, amt sdk.Coins) error
	GetBalance(ctx context.Context, addr sdk.AccAddress, denom string) sdk.Coin
	SpendableCoin(ctx context.Context, addr sdk.AccAddress, denom string) sdk.Coin
	GetSupply(ctx context.Context, denom string) sdk.Coin
}

// FeesKeeper is the subset of x/fees used to lock and release the metadata
// state deposit. The method shapes match x/fees/keeper.Keeper. ReleaseDeposit
// refunds Params.DepositRefundFraction (99% at genesis) to the owner's
// earnings and burns the rest; x/token does not reimplement that split.
type FeesKeeper interface {
	LockDeposit(ctx context.Context, owner sdk.AccAddress, id string, amount math.Int) error
	ReleaseDeposit(ctx context.Context, id string) (refund, burn math.Int, err error)
	GetDeposit(ctx context.Context, id string) (feestypes.Deposit, error)

	// FundSpendFromEarnings tops addr's bank balance up from its own earnings so CreateToken can
	// take its fee and metadata deposit (C2 item 4). Called from the message handler, after its
	// own checks, so a failed creation reverses it.
	FundSpendFromEarnings(ctx context.Context, addr sdk.AccAddress, denom string, needed math.Int) error
}

// TransferHook is the contract call a token may request at creation: the token names a CosmWasm
// contract, and the keeper calls it on every MsgTransfer with a gas meter limited to
// TransferHookGasCap. This module executes no CosmWasm itself; the app injects the implementation.
type TransferHook interface {
	// ValidateHook is called at creation: contract must be a contract that exists in this build.
	ValidateHook(ctx context.Context, contract sdk.AccAddress) error
	// OnTransfer runs the contract's hook for one transfer. An error refuses the transfer.
	OnTransfer(ctx context.Context, contract sdk.AccAddress, denom string, from, to sdk.AccAddress, amount math.Int) error
}
