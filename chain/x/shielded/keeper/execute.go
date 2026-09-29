package keeper

import (
	"fmt"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/shielded/pool"
	"github.com/DeBrosOfficial/network/chain/x/shielded/types"
)

func coins(amount math.Int) sdk.Coins {
	return sdk.NewCoins(sdk.NewCoin(params.BaseDenom, amount))
}

// burn destroys norama held by the module. A zero amount is a no-op.
func (k Keeper) burn(ctx sdk.Context, amount math.Int) error {
	if !amount.IsPositive() {
		return nil
	}
	if err := k.deps.Bank.BurnCoins(ctx, types.ModuleName, coins(amount)); err != nil {
		return fmt.Errorf("burn %s%s: %w", amount, params.BaseDenom, err)
	}
	return nil
}

// ExecuteTransfer applies an admitted signer-less transfer. The fee is the bundle's value balance:
// it leaves the pool, the base part and the nullifier fees are burned, and the rest is the tip. The
// tip goes to the proposer's earnings; with no resolvable proposer it is burned too, so nothing
// piles up in an account nobody can spend from.
func (k Keeper) ExecuteTransfer(ctx sdk.Context, adm *Admitted) error {
	if err := k.register(ctx, adm.Bundle); err != nil {
		return err
	}
	before, err := k.poolBalance(ctx, nativePool())
	if err != nil {
		return err
	}
	if err := k.debitPool(ctx, nativePool(), adm.Amount); err != nil {
		return err
	}
	burn := adm.BaseFee.Add(adm.NullifierFees)
	tip := adm.Amount.Sub(burn)
	proposer, found := k.deps.Fees.ProposerAccount(ctx)
	if !found {
		burn, tip = adm.Amount, math.ZeroInt()
	}
	if err := k.capTip(ctx, before, tip); err != nil {
		return err
	}
	if err := k.burn(ctx, burn); err != nil {
		return err
	}
	if tip.IsPositive() {
		if err := k.deps.Fees.CreditEarnings(ctx, types.ModuleName, proposer, sdk.NewCoin(params.BaseDenom, tip)); err != nil {
			return fmt.Errorf("pay the tip to the proposer: %w", err)
		}
	}
	return nil
}

// ExecuteShield applies an admitted MsgShield: the signer's bank balance funds the pool with the
// amount and pays the burned nullifier fees on top of it, so the pool holds exactly what the notes
// are worth.
func (k Keeper) ExecuteShield(ctx sdk.Context, signer sdk.AccAddress, adm *Admitted) error {
	if err := k.register(ctx, adm.Bundle); err != nil {
		return err
	}
	total := adm.Amount.Add(adm.NullifierFees)
	if err := k.deps.Bank.SendCoinsFromAccountToModule(ctx, signer, types.ModuleName, coins(total)); err != nil {
		return fmt.Errorf("move %s%s from %s into the shielded pool: %w", total, params.BaseDenom, signer, err)
	}
	return k.creditPool(ctx, adm)
}

// ExecuteShieldEarnings applies an admitted MsgShieldEarnings: the signer's own earnings fund the
// pool and the nullifier fees.
func (k Keeper) ExecuteShieldEarnings(ctx sdk.Context, signer sdk.AccAddress, adm *Admitted) error {
	if err := k.register(ctx, adm.Bundle); err != nil {
		return err
	}
	total := adm.Amount.Add(adm.NullifierFees)
	debited, err := k.deps.Fees.DebitEarningsUpTo(ctx, signer, total)
	if err != nil {
		return fmt.Errorf("debit %s's earnings: %w", signer, err)
	}
	if debited.LT(total) {
		return fmt.Errorf("%s has %s%s in earnings, shielding needs %s%s", signer, debited, params.BaseDenom, total, params.BaseDenom)
	}
	if err := k.deps.Bank.SendCoinsFromModuleToModule(ctx, k.deps.Fees.EarningsModule(), types.ModuleName, coins(total)); err != nil {
		return fmt.Errorf("move earnings into the shielded pool: %w", err)
	}
	return k.creditPool(ctx, adm)
}

// creditPool burns the per-nullifier fees and credits the pool with the shielded amount.
func (k Keeper) creditPool(ctx sdk.Context, adm *Admitted) error {
	if err := k.burn(ctx, adm.NullifierFees); err != nil {
		return err
	}
	return k.credit(ctx, nativePool(), adm.Amount)
}

// capTip counts a transfer's tip against the pool's 24h cap. The burned part of a fee is exempt, but
// the tip becomes a proposer's spendable earnings, so a proposer holding counterfeit notes could
// otherwise drain the pool through fees without meeting the cap. A tip over what the cap has left
// fails the transfer, like a fee top-up.
func (k Keeper) capTip(ctx sdk.Context, poolBefore, tip math.Int) error {
	if !tip.IsPositive() {
		return nil
	}
	if _, err := k.applyCap(ctx, ctx.BlockTime(), nativePool(), poolBefore, tip, pool.KindFeeTopup); err != nil {
		return fmt.Errorf("the tip of %s%s: %w", tip, params.BaseDenom, err)
	}
	return nil
}
