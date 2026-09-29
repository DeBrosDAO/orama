package keeper

import (
	"fmt"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/fees/types"
)

// FeeInvariants is the result of CheckInvariants.
type FeeInvariants struct {
	EarningsMatchModule bool
	DepositsMatchModule bool
	FeesBalance         bool
	Collected           math.Int
	Burned              math.Int
	Distributed         math.Int
	Detail              string
}

// CheckInvariants checks the three x/fees invariants from
// plans/open-network/track-c-chain.md C2:
//
//   - sum of earnings balances + sum of fee-only balances == the fees module account balance
//   - sum of open deposits == the deposits module account balance
//   - burned + distributed == fees collected
func (k Keeper) CheckInvariants(ctx sdk.Context) (FeeInvariants, error) {
	earningsOnly, err := k.sumBalances(ctx, k.Earnings)
	if err != nil {
		return FeeInvariants{}, err
	}
	feeOnly, err := k.sumBalances(ctx, k.FeeBalances)
	if err != nil {
		return FeeInvariants{}, err
	}
	earningsSum := earningsOnly.Add(feeOnly)
	depositSum, err := k.sumDeposits(ctx)
	if err != nil {
		return FeeInvariants{}, err
	}
	collected, err := k.feeTotal(ctx, k.Collected)
	if err != nil {
		return FeeInvariants{}, fmt.Errorf("failed to load collected fees: %w", err)
	}
	burned, err := k.feeTotal(ctx, k.Burned)
	if err != nil {
		return FeeInvariants{}, fmt.Errorf("failed to load burned fees: %w", err)
	}
	distributed, err := k.feeTotal(ctx, k.Distributed)
	if err != nil {
		return FeeInvariants{}, fmt.Errorf("failed to load distributed fees: %w", err)
	}

	earningsBalance := k.bankKeeper.GetBalance(ctx, authtypes.NewModuleAddress(types.ModuleName), params.BaseDenom).Amount
	depositBalance := k.bankKeeper.GetBalance(ctx, authtypes.NewModuleAddress(types.DepositsModuleName), params.BaseDenom).Amount
	earningsMatch := earningsSum.Equal(earningsBalance)
	depositsMatch := depositSum.Equal(depositBalance)
	feesBalance := burned.Add(distributed).Equal(collected)

	detail := fmt.Sprintf(
		"earnings + fee balances match module: %t (ledger=%s of which fee-only=%s module=%s)\n"+
			"deposits match module: %t (ledger=%s module=%s)\n"+
			"burned + distributed == collected: %t (burned=%s distributed=%s collected=%s)\n",
		earningsMatch, earningsSum, feeOnly, earningsBalance,
		depositsMatch, depositSum, depositBalance,
		feesBalance, burned, distributed, collected,
	)
	return FeeInvariants{
		EarningsMatchModule: earningsMatch,
		DepositsMatchModule: depositsMatch,
		FeesBalance:         feesBalance,
		Collected:           collected,
		Burned:              burned,
		Distributed:         distributed,
		Detail:              detail,
	}, nil
}

func (k Keeper) sumBalances(ctx sdk.Context, ledger collections.Map[string, math.Int]) (math.Int, error) {
	sum := math.ZeroInt()
	err := ledger.Walk(ctx, nil, func(_ string, balance math.Int) (bool, error) {
		sum = sum.Add(balance)
		return false, nil
	})
	if err != nil {
		return math.Int{}, fmt.Errorf("failed to sum balances: %w", err)
	}
	return sum, nil
}

func (k Keeper) sumDeposits(ctx sdk.Context) (math.Int, error) {
	sum := math.ZeroInt()
	err := k.Deposits.Walk(ctx, nil, func(_ string, deposit types.Deposit) (bool, error) {
		sum = sum.Add(deposit.Amount)
		return false, nil
	})
	if err != nil {
		return math.Int{}, fmt.Errorf("failed to sum deposits: %w", err)
	}
	return sum, nil
}
