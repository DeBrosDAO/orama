package keeper

import (
	"context"
	"errors"
	"fmt"

	errorsmod "cosmossdk.io/errors"
	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"

	"github.com/DeBrosOfficial/network/chain/x/storage/types"
)

// reject marks err as a failure of the one item being processed (see types.Refuse).
func reject(err error) error { return types.Refuse(err) }

// rejectf builds a new item failure.
func rejectf(format string, args ...any) error {
	return types.Refuse(fmt.Errorf(format, args...))
}

// refuse marks a collaborator's error as an item failure only when it is a refusal the collaborator
// makes about that one item: funds that cannot cover the write, a blocked or unauthorized account,
// or a record that is not there (the SDK errors bank and x/fees return for those), and anything
// the collaborator's adapter already marked with types.Refuse (x/nodes: the node is gone or not
// active). Every other collaborator error (a collection that cannot be read or decoded, a broken
// counter, an invariant a collaborator reports) is a fault of the state machine and reaches
// isolate unmarked, so the block fails instead of running on state it cannot trust.
func refuse(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, types.ErrItemRejected) || errorsmod.IsOf(err, sdkerrors.ErrInsufficientFunds, sdkerrors.ErrUnauthorized, sdkerrors.ErrNotFound) {
		return reject(err)
	}
	return err
}

// Every collaborator module (bank, x/fees, x/emission, x/nodes) runs its writes atomically and
// returns an error when it refuses one. The wrappers below pass every collaborator error through
// refuse, so only the refusals it recognizes become item rejections. Errors from x/storage's own
// collections are never marked here: they reach isolate unmarked and fail the block.

type boundBank struct{ types.BankKeeper }

func (b boundBank) SendCoinsFromAccountToModule(ctx context.Context, from sdk.AccAddress, module string, amt sdk.Coins) error {
	return refuse(b.BankKeeper.SendCoinsFromAccountToModule(ctx, from, module, amt))
}

func (b boundBank) SendCoinsFromModuleToModule(ctx context.Context, from, to string, amt sdk.Coins) error {
	return refuse(b.BankKeeper.SendCoinsFromModuleToModule(ctx, from, to, amt))
}

func (b boundBank) SendCoinsFromModuleToAccount(ctx context.Context, module string, to sdk.AccAddress, amt sdk.Coins) error {
	return refuse(b.BankKeeper.SendCoinsFromModuleToAccount(ctx, module, to, amt))
}

func (b boundBank) BurnCoins(ctx context.Context, module string, amt sdk.Coins) error {
	return refuse(b.BankKeeper.BurnCoins(ctx, module, amt))
}

type boundEarnings struct{ types.EarningsKeeper }

func (b boundEarnings) CreditEarnings(ctx context.Context, module string, addr sdk.AccAddress, amt sdk.Coin) error {
	return refuse(b.EarningsKeeper.CreditEarnings(ctx, module, addr, amt))
}

func (b boundEarnings) FundSpendFromEarnings(ctx context.Context, addr sdk.AccAddress, denom string, needed math.Int) error {
	return refuse(b.EarningsKeeper.FundSpendFromEarnings(ctx, addr, denom, needed))
}

type boundDeposits struct{ types.DepositKeeper }

func (b boundDeposits) LockDeposit(ctx context.Context, owner sdk.AccAddress, id string, amount math.Int) error {
	return refuse(b.DepositKeeper.LockDeposit(ctx, owner, id, amount))
}

func (b boundDeposits) TopUpDeposit(ctx context.Context, id string, extra math.Int) error {
	return refuse(b.DepositKeeper.TopUpDeposit(ctx, id, extra))
}

func (b boundDeposits) DepositAmount(ctx context.Context, id string) (math.Int, bool, error) {
	amount, found, err := b.DepositKeeper.DepositAmount(ctx, id)
	return amount, found, refuse(err)
}

func (b boundDeposits) ReleaseDeposit(ctx context.Context, id string) (math.Int, math.Int, error) {
	refund, burn, err := b.DepositKeeper.ReleaseDeposit(ctx, id)
	return refund, burn, refuse(err)
}

func (b boundDeposits) SlashDeposit(ctx context.Context, id string, amount math.Int) (math.Int, error) {
	burned, err := b.DepositKeeper.SlashDeposit(ctx, id, amount)
	return burned, refuse(err)
}

type boundEmission struct{ types.EmissionKeeper }

func (b boundEmission) CurrentEpoch(ctx context.Context) (uint64, error) {
	epoch, err := b.EmissionKeeper.CurrentEpoch(ctx)
	return epoch, refuse(err)
}

func (b boundEmission) StorageCeiling(ctx context.Context, epoch uint64) (math.Int, error) {
	ceiling, err := b.EmissionKeeper.StorageCeiling(ctx, epoch)
	return ceiling, refuse(err)
}

func (b boundEmission) MintStorageService(ctx context.Context, epoch uint64, amt math.Int) error {
	return refuse(b.EmissionKeeper.MintStorageService(ctx, epoch, amt))
}

type boundNodes struct{ types.NodeView }

func (b boundNodes) IsActive(ctx context.Context, id string) (bool, error) {
	v, err := b.NodeView.IsActive(ctx, id)
	return v, refuse(err)
}

func (b boundNodes) IsProbation(ctx context.Context, id string) (bool, error) {
	v, err := b.NodeView.IsProbation(ctx, id)
	return v, refuse(err)
}

func (b boundNodes) TakeStorageChanges(ctx context.Context) ([]string, error) {
	ids, err := b.NodeView.TakeStorageChanges(ctx)
	return ids, refuse(err)
}

func (b boundNodes) MarkStorageChanged(ctx context.Context, id string) error {
	return refuse(b.NodeView.MarkStorageChanged(ctx, id))
}

func (b boundNodes) HotKey(ctx context.Context, id string) (sdk.AccAddress, error) {
	v, err := b.NodeView.HotKey(ctx, id)
	return v, refuse(err)
}

func (b boundNodes) Operator(ctx context.Context, id string) (string, error) {
	v, err := b.NodeView.Operator(ctx, id)
	return v, refuse(err)
}

func (b boundNodes) Network16(ctx context.Context, id string) (string, error) {
	v, err := b.NodeView.Network16(ctx, id)
	return v, refuse(err)
}

func (b boundNodes) ASN(ctx context.Context, id string) (uint32, error) {
	v, err := b.NodeView.ASN(ctx, id)
	return v, refuse(err)
}

func (b boundNodes) DeclaredCapacity(ctx context.Context, id string) (uint64, error) {
	v, err := b.NodeView.DeclaredCapacity(ctx, id)
	return v, refuse(err)
}

func (b boundNodes) Slash(ctx context.Context, id string, amount math.Int) error {
	return refuse(b.NodeView.Slash(ctx, id, amount))
}

func (b boundNodes) Jail(ctx context.Context, id string) error {
	return refuse(b.NodeView.Jail(ctx, id))
}
