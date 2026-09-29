package keeper

import (
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/storage/types"
)

// rejection wraps an error so it matches types.ErrItemRejected while keeping the original message
// and chain (transaction result codes still resolve through Unwrap).
type rejection struct{ err error }

func (r rejection) Error() string        { return r.err.Error() }
func (r rejection) Unwrap() error        { return r.err }
func (r rejection) Is(target error) bool { return target == types.ErrItemRejected }

// reject marks err as a failure of the one item being processed. A collections encoding fault is
// never an item's failure: the state itself cannot be read, and it stays fatal.
func reject(err error) error {
	if err == nil || errors.Is(err, collections.ErrEncoding) {
		return err
	}
	return rejection{err}
}

// rejectf builds a new item failure.
func rejectf(format string, args ...any) error {
	return rejection{fmt.Errorf(format, args...)}
}

// Every collaborator module (bank, x/fees, x/emission, x/nodes) runs its writes atomically and
// returns an error when it refuses one. To x/storage such a refusal is data about the item that
// asked, so the wrappers below mark every collaborator error as an item rejection. Errors from
// x/storage's own collections are not marked: they reach isolate unmarked and fail the block.

type boundBank struct{ types.BankKeeper }

func (b boundBank) SendCoinsFromAccountToModule(ctx context.Context, from sdk.AccAddress, module string, amt sdk.Coins) error {
	return reject(b.BankKeeper.SendCoinsFromAccountToModule(ctx, from, module, amt))
}

func (b boundBank) SendCoinsFromModuleToModule(ctx context.Context, from, to string, amt sdk.Coins) error {
	return reject(b.BankKeeper.SendCoinsFromModuleToModule(ctx, from, to, amt))
}

func (b boundBank) SendCoinsFromModuleToAccount(ctx context.Context, module string, to sdk.AccAddress, amt sdk.Coins) error {
	return reject(b.BankKeeper.SendCoinsFromModuleToAccount(ctx, module, to, amt))
}

func (b boundBank) BurnCoins(ctx context.Context, module string, amt sdk.Coins) error {
	return reject(b.BankKeeper.BurnCoins(ctx, module, amt))
}

type boundEarnings struct{ types.EarningsKeeper }

func (b boundEarnings) CreditEarnings(ctx context.Context, module string, addr sdk.AccAddress, amt sdk.Coin) error {
	return reject(b.EarningsKeeper.CreditEarnings(ctx, module, addr, amt))
}

func (b boundEarnings) FundSpendFromEarnings(ctx context.Context, addr sdk.AccAddress, denom string, needed math.Int) error {
	return reject(b.EarningsKeeper.FundSpendFromEarnings(ctx, addr, denom, needed))
}

type boundDeposits struct{ types.DepositKeeper }

func (b boundDeposits) LockDeposit(ctx context.Context, owner sdk.AccAddress, id string, amount math.Int) error {
	return reject(b.DepositKeeper.LockDeposit(ctx, owner, id, amount))
}

func (b boundDeposits) TopUpDeposit(ctx context.Context, id string, extra math.Int) error {
	return reject(b.DepositKeeper.TopUpDeposit(ctx, id, extra))
}

func (b boundDeposits) DepositAmount(ctx context.Context, id string) (math.Int, bool, error) {
	amount, found, err := b.DepositKeeper.DepositAmount(ctx, id)
	return amount, found, reject(err)
}

func (b boundDeposits) ReleaseDeposit(ctx context.Context, id string) (math.Int, math.Int, error) {
	refund, burn, err := b.DepositKeeper.ReleaseDeposit(ctx, id)
	return refund, burn, reject(err)
}

func (b boundDeposits) SlashDeposit(ctx context.Context, id string, amount math.Int) (math.Int, error) {
	burned, err := b.DepositKeeper.SlashDeposit(ctx, id, amount)
	return burned, reject(err)
}

type boundEmission struct{ types.EmissionKeeper }

func (b boundEmission) CurrentEpoch(ctx context.Context) (uint64, error) {
	epoch, err := b.EmissionKeeper.CurrentEpoch(ctx)
	return epoch, reject(err)
}

func (b boundEmission) StorageCeiling(ctx context.Context, epoch uint64) (math.Int, error) {
	ceiling, err := b.EmissionKeeper.StorageCeiling(ctx, epoch)
	return ceiling, reject(err)
}

func (b boundEmission) MintStorageService(ctx context.Context, epoch uint64, amt math.Int) error {
	return reject(b.EmissionKeeper.MintStorageService(ctx, epoch, amt))
}

type boundNodes struct{ types.NodeView }

func (b boundNodes) IsActive(ctx context.Context, id string) (bool, error) {
	v, err := b.NodeView.IsActive(ctx, id)
	return v, reject(err)
}

func (b boundNodes) IsProbation(ctx context.Context, id string) (bool, error) {
	v, err := b.NodeView.IsProbation(ctx, id)
	return v, reject(err)
}

func (b boundNodes) TakeStorageChanges(ctx context.Context) ([]string, error) {
	ids, err := b.NodeView.TakeStorageChanges(ctx)
	return ids, reject(err)
}

func (b boundNodes) MarkStorageChanged(ctx context.Context, id string) error {
	return reject(b.NodeView.MarkStorageChanged(ctx, id))
}

func (b boundNodes) HotKey(ctx context.Context, id string) (sdk.AccAddress, error) {
	v, err := b.NodeView.HotKey(ctx, id)
	return v, reject(err)
}

func (b boundNodes) Operator(ctx context.Context, id string) (string, error) {
	v, err := b.NodeView.Operator(ctx, id)
	return v, reject(err)
}

func (b boundNodes) Network16(ctx context.Context, id string) (string, error) {
	v, err := b.NodeView.Network16(ctx, id)
	return v, reject(err)
}

func (b boundNodes) ASN(ctx context.Context, id string) (uint32, error) {
	v, err := b.NodeView.ASN(ctx, id)
	return v, reject(err)
}

func (b boundNodes) DeclaredCapacity(ctx context.Context, id string) (uint64, error) {
	v, err := b.NodeView.DeclaredCapacity(ctx, id)
	return v, reject(err)
}

func (b boundNodes) Slash(ctx context.Context, id string, amount math.Int) error {
	return reject(b.NodeView.Slash(ctx, id, amount))
}

func (b boundNodes) Jail(ctx context.Context, id string) error {
	return reject(b.NodeView.Jail(ctx, id))
}
