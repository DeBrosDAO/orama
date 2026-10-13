package keeper

import (
	"fmt"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/fees/types"
)

// InitGenesis sets x/fees's state from a GenesisState.
func (k Keeper) InitGenesis(ctx sdk.Context, genState types.GenesisState) error {
	if err := genState.Validate(); err != nil {
		return fmt.Errorf("invalid fees genesis state: %w", err)
	}

	if err := k.Params.Set(ctx, genState.Params); err != nil {
		return fmt.Errorf("failed to set fees params: %w", err)
	}
	if err := k.BaseFee.Set(ctx, genState.BaseFee); err != nil {
		return fmt.Errorf("failed to set base fee: %w", err)
	}
	for _, e := range genState.EarningsAccounts {
		if err := k.Earnings.Set(ctx, e.Address, e.Balance); err != nil {
			return fmt.Errorf("failed to set earnings account %q: %w", e.Address, err)
		}
	}
	for _, e := range genState.FeeBalances {
		if err := k.FeeBalances.Set(ctx, e.Address, e.Balance); err != nil {
			return fmt.Errorf("failed to set fee balance %q: %w", e.Address, err)
		}
	}
	for _, d := range genState.Deposits {
		if err := k.Deposits.Set(ctx, d.Id, d); err != nil {
			return fmt.Errorf("failed to set deposit %q: %w", d.Id, err)
		}
	}
	if err := k.Collected.Set(ctx, types.NormalizeFeeTotal(genState.CumulativeCollected)); err != nil {
		return fmt.Errorf("failed to set cumulative collected fees: %w", err)
	}
	if err := k.Burned.Set(ctx, types.NormalizeFeeTotal(genState.CumulativeBurned)); err != nil {
		return fmt.Errorf("failed to set cumulative burned fees: %w", err)
	}
	if err := k.Distributed.Set(ctx, types.NormalizeFeeTotal(genState.CumulativeDistributed)); err != nil {
		return fmt.Errorf("failed to set cumulative distributed fees: %w", err)
	}

	return nil
}

// ExportGenesis reads x/fees's current state back into a GenesisState.
func (k Keeper) ExportGenesis(ctx sdk.Context) (*types.GenesisState, error) {
	p, err := k.Params.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get fees params: %w", err)
	}
	baseFee, err := k.BaseFee.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get base fee: %w", err)
	}

	var earnings []types.EarningsAccount
	if err := k.Earnings.Walk(ctx, nil, func(addr string, balance math.Int) (bool, error) {
		earnings = append(earnings, types.EarningsAccount{Address: addr, Balance: balance})
		return false, nil
	}); err != nil {
		return nil, fmt.Errorf("failed to walk earnings accounts: %w", err)
	}

	var feeBalances []types.EarningsAccount
	if err := k.FeeBalances.Walk(ctx, nil, func(addr string, balance math.Int) (bool, error) {
		feeBalances = append(feeBalances, types.EarningsAccount{Address: addr, Balance: balance})
		return false, nil
	}); err != nil {
		return nil, fmt.Errorf("failed to walk fee balances: %w", err)
	}

	var deposits []types.Deposit
	if err := k.Deposits.Walk(ctx, nil, func(_ string, d types.Deposit) (bool, error) {
		deposits = append(deposits, d)
		return false, nil
	}); err != nil {
		return nil, fmt.Errorf("failed to walk deposits: %w", err)
	}

	collected, err := k.feeTotal(ctx, k.Collected)
	if err != nil {
		return nil, fmt.Errorf("failed to get cumulative collected fees: %w", err)
	}
	burned, err := k.feeTotal(ctx, k.Burned)
	if err != nil {
		return nil, fmt.Errorf("failed to get cumulative burned fees: %w", err)
	}
	distributed, err := k.feeTotal(ctx, k.Distributed)
	if err != nil {
		return nil, fmt.Errorf("failed to get cumulative distributed fees: %w", err)
	}

	return &types.GenesisState{
		Params:                p,
		BaseFee:               baseFee,
		EarningsAccounts:      earnings,
		FeeBalances:           feeBalances,
		Deposits:              deposits,
		CumulativeCollected:   collected,
		CumulativeBurned:      burned,
		CumulativeDistributed: distributed,
	}, nil
}
