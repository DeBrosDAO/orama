package keeper

import (
	"fmt"

	"cosmossdk.io/collections"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/archive/types"
)

// InitGenesis writes x/archive's state from a GenesisState.
func (k Keeper) InitGenesis(ctx sdk.Context, gs types.GenesisState) error {
	if err := gs.Validate(); err != nil {
		return fmt.Errorf("invalid archive genesis state: %w", err)
	}
	if err := k.Params.Set(ctx, gs.Params); err != nil {
		return fmt.Errorf("failed to set archive params: %w", err)
	}
	for _, rec := range gs.Ranges {
		rec.BundleHash = append([]byte(nil), rec.BundleHash...)
		rec.MerkleRoot = append([]byte(nil), rec.MerkleRoot...)
		rec.DealIds = append([]string(nil), rec.DealIds...)
		rec.Archivers = append([]string(nil), rec.Archivers...)
		if err := k.Ranges.Set(ctx, collections.Join(rec.StartHeight, rec.EndHeight), rec); err != nil {
			return fmt.Errorf("failed to set range %d-%d: %w", rec.StartHeight, rec.EndHeight, err)
		}
	}
	got, err := k.recomputeLastArchived(ctx)
	if err != nil {
		return err
	}
	if got != gs.LastArchivedHeight {
		return fmt.Errorf("last archived height %d does not match ranges (%d)", gs.LastArchivedHeight, got)
	}
	if err := k.LastArchivedHeight.Set(ctx, got); err != nil {
		return fmt.Errorf("failed to set last archived height: %w", err)
	}
	return nil
}

// ExportGenesis reads x/archive's current state back into a GenesisState.
func (k Keeper) ExportGenesis(ctx sdk.Context) (*types.GenesisState, error) {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get archive params: %w", err)
	}
	var ranges []types.RangeRecord
	if err := k.Ranges.Walk(ctx, nil, func(_ collections.Pair[int64, int64], rec types.RangeRecord) (bool, error) {
		ranges = append(ranges, rec)
		return false, nil
	}); err != nil {
		return nil, fmt.Errorf("failed to walk ranges: %w", err)
	}
	if ranges == nil {
		ranges = []types.RangeRecord{}
	}
	last, err := k.LastArchivedHeight.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get last archived height: %w", err)
	}
	if recomputed := types.ContiguousArchivedHeight(ranges); recomputed != last {
		return nil, fmt.Errorf("stored last archived height %d does not match ranges (%d)", last, recomputed)
	}
	return &types.GenesisState{
		Params:             params,
		Ranges:             ranges,
		LastArchivedHeight: last,
	}, nil
}
