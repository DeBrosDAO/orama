package app

import (
	"context"
	"fmt"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
)

// slashableDepartedTokens is the initial balance of unbonding and redelegation
// entries that stock Slash will charge against the slash budget: created at or
// after infractionHeight, and not yet mature. A slash at the current height
// does not scan those entries, so this returns zero there.
func (s slashBaseFix) slashableDepartedTokens(ctx context.Context, validator stakingtypes.Validator, infractionHeight int64) (math.Int, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	if infractionHeight >= sdkCtx.BlockHeight() {
		return math.ZeroInt(), nil
	}
	operator, err := s.ValidatorAddressCodec().StringToBytes(validator.GetOperator())
	if err != nil {
		return math.Int{}, fmt.Errorf("slashBaseFix: invalid operator address %q: %w", validator.GetOperator(), err)
	}
	now := sdkCtx.BlockTime()
	total := math.ZeroInt()

	unbondings, err := s.GetUnbondingDelegationsFromValidator(ctx, operator)
	if err != nil {
		return math.Int{}, fmt.Errorf("slashBaseFix: failed to load unbonding delegations for %q: %w", validator.GetOperator(), err)
	}
	for _, unbonding := range unbondings {
		for _, entry := range unbonding.Entries {
			if !slashEntryCounts(entry.CreationHeight, entry.IsMature(now), entry.OnHold(), infractionHeight) {
				continue
			}
			total = total.Add(entry.InitialBalance)
		}
	}

	redelegations, err := s.GetRedelegationsFromSrcValidator(ctx, operator)
	if err != nil {
		return math.Int{}, fmt.Errorf("slashBaseFix: failed to load redelegations from %q: %w", validator.GetOperator(), err)
	}
	for _, redelegation := range redelegations {
		for _, entry := range redelegation.Entries {
			if !slashEntryCounts(entry.CreationHeight, entry.IsMature(now), entry.OnHold(), infractionHeight) {
				continue
			}
			total = total.Add(entry.InitialBalance)
		}
	}
	return total, nil
}

func slashEntryCounts(creationHeight int64, mature, onHold bool, infractionHeight int64) bool {
	if creationHeight < infractionHeight {
		return false
	}
	if mature && !onHold {
		return false
	}
	return true
}
