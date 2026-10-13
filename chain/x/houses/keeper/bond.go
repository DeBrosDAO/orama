package keeper

import (
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/houses/types"
)

// LockHouseBond adds amount to the signer's locked house bond.
func (k Keeper) LockHouseBond(ctx sdk.Context, signer sdk.AccAddress, amount math.Int) error {
	if amount.IsNil() || !amount.IsPositive() {
		return fmt.Errorf("house bond lock must be positive, got %s", amount)
	}
	coins := sdk.NewCoins(sdk.NewCoin(params.BaseDenom, amount))
	if err := k.bank.SendCoinsFromAccountToModule(ctx, signer, types.ModuleName, coins); err != nil {
		return fmt.Errorf("failed to lock house bond: %w", err)
	}
	bond, err := k.Bonds.Get(ctx, signer.String())
	if err != nil {
		if !errors.Is(err, collections.ErrNotFound) {
			return fmt.Errorf("failed to load house bond: %w", err)
		}
		bond = types.HouseBond{Address: signer.String(), Amount: amount, LockedAtHeight: ctx.BlockHeight()}
		if err := k.Bonds.Set(ctx, signer.String(), bond); err != nil {
			return fmt.Errorf("failed to store house bond: %w", err)
		}
		return nil
	}
	bond.Amount = types.IntOrZero(bond.Amount).Add(amount)
	if err := k.Bonds.Set(ctx, signer.String(), bond); err != nil {
		return fmt.Errorf("failed to store house bond: %w", err)
	}
	return nil
}

// UnlockHouseBond returns the full bond. It fails while the signer has an
// operator vote on a proposal that is still being voted or vetoed.
func (k Keeper) UnlockHouseBond(ctx sdk.Context, signer sdk.AccAddress) error {
	if err := k.bondLockedByVote(ctx, signer.String()); err != nil {
		return err
	}
	bond, err := k.Bonds.Get(ctx, signer.String())
	if err != nil {
		return fmt.Errorf("failed to load house bond: %w", err)
	}
	coins := sdk.NewCoins(sdk.NewCoin(params.BaseDenom, bond.Amount))
	if err := k.bank.SendCoinsFromModuleToAccount(ctx, types.ModuleName, signer, coins); err != nil {
		return fmt.Errorf("failed to return house bond: %w", err)
	}
	if err := k.Bonds.Remove(ctx, signer.String()); err != nil {
		return fmt.Errorf("failed to clear house bond: %w", err)
	}
	return nil
}

func (k Keeper) bondLockedByVote(ctx context.Context, addr string) error {
	ids, err := k.activeIDs(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		p, err := k.getProposal(ctx, id)
		if err != nil {
			return err
		}
		if p.Status != types.ProposalStatus_VOTING && p.Status != types.ProposalStatus_VETO_WINDOW {
			continue
		}
		has, err := k.OperatorVotes.Has(ctx, collections.Join(id, addr))
		if err != nil {
			return fmt.Errorf("failed to check operator vote: %w", err)
		}
		if has {
			return fmt.Errorf("%w", types.ErrBondLocked)
		}
	}
	return nil
}

func (k Keeper) slashBond(ctx context.Context, signer sdk.AccAddress) error {
	bond, err := k.Bonds.Get(ctx, signer.String())
	if err != nil {
		return fmt.Errorf("failed to load house bond for slash: %w", err)
	}
	coins := sdk.NewCoins(sdk.NewCoin(params.BaseDenom, bond.Amount))
	if err := k.bank.BurnCoins(ctx, types.ModuleName, coins); err != nil {
		return fmt.Errorf("failed to burn house bond: %w", err)
	}
	if err := k.Bonds.Remove(ctx, signer.String()); err != nil {
		return fmt.Errorf("failed to remove slashed house bond: %w", err)
	}
	return nil
}
