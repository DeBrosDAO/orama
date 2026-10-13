package keeper

import (
	"context"
	"fmt"

	errorsmod "cosmossdk.io/errors"
	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/fees/types"
)

// BurnFromBalance destroys amount of from's own bank balance. It is the burn path for a protocol
// charge that is paid and gone (the per-byte code fee of x/wasmpolicy): the coins go to x/fees's module
// account, which holds the Burner permission, and are burned there, so x/emission's ReconcileBurns books
// the drop in the norama supply as a burn in the same block. Earnings never fund it: it checks the
// spendable bank balance first and moves nothing when that is short.
func (k Keeper) BurnFromBalance(ctx context.Context, from sdk.AccAddress, amount math.Int, reason string) error {
	if amount.IsNil() || !amount.IsPositive() {
		return fmt.Errorf("burn amount for %s must be positive, got %s", reason, amount)
	}
	spendable := k.bankKeeper.SpendableCoins(ctx, from).AmountOf(params.BaseDenom)
	if spendable.LT(amount) {
		return errorsmod.Wrapf(sdkerrors.ErrInsufficientFunds,
			"cannot pay %s: need %s%s, have %s%s spendable in the bank balance of %s",
			reason, amount, params.BaseDenom, spendable, params.BaseDenom, from)
	}
	coins := sdk.NewCoins(sdk.NewCoin(params.BaseDenom, amount))
	if err := k.bankKeeper.SendCoinsFromAccountToModule(ctx, from, types.ModuleName, coins); err != nil {
		return fmt.Errorf("failed to take %s from %s: %w", reason, from, err)
	}
	if err := k.bankKeeper.BurnCoins(ctx, types.ModuleName, coins); err != nil {
		return fmt.Errorf("failed to burn %s: %w", reason, err)
	}
	return nil
}
