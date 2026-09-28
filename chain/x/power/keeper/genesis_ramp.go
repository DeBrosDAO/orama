package keeper

import (
	"fmt"
	"sort"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/power/types"
)

func (k Keeper) importRampRecord(ctx sdk.Context, record types.ValidatorRampRecord) error {
	if record.ActivationSet {
		if err := k.RampActivation.Set(ctx, record.OperatorAddress, record.ActivationEpoch); err != nil {
			return fmt.Errorf("failed to set ramp record for %q: %w", record.OperatorAddress, err)
		}
	}
	if !record.AdmittedTokens.IsNil() && record.AdmittedTokens.IsPositive() {
		if err := k.RampAdmitted.Set(ctx, record.OperatorAddress, record.AdmittedTokens); err != nil {
			return fmt.Errorf("failed to set admitted bond for %q: %w", record.OperatorAddress, err)
		}
	}
	if !record.ExcessTokens.IsNil() && record.ExcessTokens.IsPositive() {
		if err := k.RampExcess.Set(ctx, record.OperatorAddress, record.ExcessTokens); err != nil {
			return fmt.Errorf("failed to set ramping bond for %q: %w", record.OperatorAddress, err)
		}
	}
	if record.ExcessEpochSet {
		if err := k.RampExcessEpoch.Set(ctx, record.OperatorAddress, record.ExcessEpoch); err != nil {
			return fmt.Errorf("failed to set ramp epoch for %q: %w", record.OperatorAddress, err)
		}
	}
	return nil
}

func (k Keeper) exportRampRecords(ctx sdk.Context) ([]types.ValidatorRampRecord, error) {
	records := map[string]*types.ValidatorRampRecord{}
	ensure := func(addr string) *types.ValidatorRampRecord {
		record, ok := records[addr]
		if ok {
			return record
		}
		record = &types.ValidatorRampRecord{
			OperatorAddress: addr,
			AdmittedTokens:  math.ZeroInt(),
			ExcessTokens:    math.ZeroInt(),
		}
		records[addr] = record
		return record
	}
	if err := k.RampActivation.Walk(ctx, nil, func(addr string, epoch uint64) (bool, error) {
		record := ensure(addr)
		record.ActivationEpoch = epoch
		record.ActivationSet = true
		return false, nil
	}); err != nil {
		return nil, fmt.Errorf("failed to walk ramp activations: %w", err)
	}
	if err := k.RampAdmitted.Walk(ctx, nil, func(addr string, amount math.Int) (bool, error) {
		ensure(addr).AdmittedTokens = amount
		return false, nil
	}); err != nil {
		return nil, fmt.Errorf("failed to walk admitted bonds: %w", err)
	}
	if err := k.RampExcess.Walk(ctx, nil, func(addr string, amount math.Int) (bool, error) {
		ensure(addr).ExcessTokens = amount
		return false, nil
	}); err != nil {
		return nil, fmt.Errorf("failed to walk ramping bonds: %w", err)
	}
	if err := k.RampExcessEpoch.Walk(ctx, nil, func(addr string, epoch uint64) (bool, error) {
		record := ensure(addr)
		record.ExcessEpoch = epoch
		record.ExcessEpochSet = true
		return false, nil
	}); err != nil {
		return nil, fmt.Errorf("failed to walk ramp epochs: %w", err)
	}
	addrs := make([]string, 0, len(records))
	for addr := range records {
		addrs = append(addrs, addr)
	}
	sort.Strings(addrs)
	out := make([]types.ValidatorRampRecord, 0, len(addrs))
	for _, addr := range addrs {
		out = append(out, *records[addr])
	}
	return out, nil
}
