package app

import (
	"context"
	"fmt"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	bankkeeper "github.com/cosmos/cosmos-sdk/x/bank/keeper"
	stakingkeeper "github.com/cosmos/cosmos-sdk/x/staking/keeper"

	"github.com/DeBrosOfficial/network/chain/app/params"
	housetypes "github.com/DeBrosOfficial/network/chain/x/houses/types"
	nodeskeeper "github.com/DeBrosOfficial/network/chain/x/nodes/keeper"
	nodestypes "github.com/DeBrosOfficial/network/chain/x/nodes/types"
	powerkeeper "github.com/DeBrosOfficial/network/chain/x/power/keeper"
)

// houseStaking reads bonded stake for the token house. Amounts are norama,
// converted from shares, not the share count itself.
type houseStaking struct {
	staking *stakingkeeper.Keeper
	bank    bankkeeper.BaseKeeper
}

func (h houseStaking) TotalBondedTokens(ctx context.Context) (math.Int, error) {
	pool := h.staking.GetBondedPool(ctx)
	if pool == nil {
		return math.Int{}, fmt.Errorf("bonded pool is not created")
	}
	return h.bank.GetBalance(ctx, pool.GetAddress(), params.BaseDenom).Amount, nil
}

func (h houseStaking) Delegations(ctx context.Context) ([]housetypes.BondedDelegation, error) {
	dels, err := h.staking.GetAllDelegations(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]housetypes.BondedDelegation, 0, len(dels))
	for _, del := range dels {
		valAddr, err := sdk.ValAddressFromBech32(del.ValidatorAddress)
		if err != nil {
			return nil, err
		}
		val, err := h.staking.GetValidator(ctx, valAddr)
		if err != nil {
			return nil, err
		}
		delegator, err := sdk.AccAddressFromBech32(del.DelegatorAddress)
		if err != nil {
			return nil, err
		}
		out = append(out, housetypes.BondedDelegation{
			Delegator: delegator,
			Validator: sdk.AccAddress(valAddr),
			Amount:    val.TokensFromShares(del.Shares).TruncateInt(),
		})
	}
	return out, nil
}

// housePower reads the lambda x/power already stored. It does not recompute it.
type housePower struct {
	power powerkeeper.Keeper
}

func (h housePower) Lambda(ctx context.Context) (math.LegacyDec, error) {
	return h.power.Lambda.Get(ctx)
}

// houseOperators lists x/nodes operators. Prefix16 and ASN are empty because
// x/nodes does not store a public network identity. x/houses skips an operator
// with an empty prefix or a zero ASN, so the operator house stays closed
// until that identity exists.
type houseOperators struct {
	nodes nodeskeeper.Keeper
}

func (h houseOperators) Operators(ctx context.Context) ([]housetypes.OperatorInfo, error) {
	var out []housetypes.OperatorInfo
	err := h.nodes.Operators.Walk(ctx, nil, func(key string, op nodestypes.Operator) (bool, error) {
		addr := op.Address
		if addr == "" {
			addr = key
		}
		acc, err := sdk.AccAddressFromBech32(addr)
		if err != nil {
			return false, err
		}
		days, err := h.nodes.OperatorServiceDays(sdk.UnwrapSDKContext(ctx), addr)
		if err != nil {
			return false, err
		}
		out = append(out, housetypes.OperatorInfo{
			Address:     acc,
			ServiceDays: days,
		})
		return false, nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
