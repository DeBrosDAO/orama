package keeper

import (
	"context"
	"fmt"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/fees/types"
)

// WithdrawEarnings moves exactly amount from addr's earnings to addr's own bank balance, where it
// is an ordinary spendable balance. The destination is the owner and cannot be chosen, so this
// never aims earnings at another address. It fails, moving nothing, when addr's earnings hold
// less than amount.
//
// The earnings ledger is debited and the coins move out of x/fees's module account in the same
// call, so "sum of earnings balances + fee balances == the fees module balance" keeps holding.
func (k Keeper) WithdrawEarnings(ctx context.Context, addr sdk.AccAddress, amount math.Int) error {
	if amount.IsNil() || !amount.IsPositive() {
		return fmt.Errorf("earnings withdrawal must be positive, got %s", amount)
	}
	balance, err := k.GetEarnings(ctx, addr)
	if err != nil {
		return err
	}
	if balance.LT(amount) {
		return fmt.Errorf("insufficient earnings: %s holds %s norama of earnings, cannot withdraw %s", addr, balance, amount)
	}
	if err := k.setEarnings(ctx, addr, balance.Sub(amount)); err != nil {
		return fmt.Errorf("failed to debit earnings for %s: %w", addr, err)
	}
	coins := sdk.NewCoins(sdk.NewCoin(params.BaseDenom, amount))
	if err := k.bankKeeper.SendCoinsFromModuleToAccount(ctx, types.ModuleName, addr, coins); err != nil {
		return fmt.Errorf("failed to move %s of %s's earnings to its bank balance: %w", amount, addr, err)
	}
	return nil
}
