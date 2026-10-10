package keeper

import (
	"fmt"

	"cosmossdk.io/collections"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/cnft/types"
)

// InitGenesis writes x/cnft's genesis state.
func (k Keeper) InitGenesis(ctx sdk.Context, genState types.GenesisState) error {
	if err := genState.Validate(); err != nil {
		return fmt.Errorf("invalid cnft genesis state: %w", err)
	}
	if err := k.NextCollectionID.Set(ctx, genState.NextCollectionId); err != nil {
		return fmt.Errorf("failed to set next collection id: %w", err)
	}
	if err := k.NextTreeID.Set(ctx, genState.NextTreeId); err != nil {
		return fmt.Errorf("failed to set next tree id: %w", err)
	}
	for _, col := range genState.Collections {
		if err := k.Collections.Set(ctx, col.Id, col); err != nil {
			return fmt.Errorf("failed to set collection %d: %w", col.Id, err)
		}
	}
	for _, tree := range genState.Trees {
		if err := k.Trees.Set(ctx, tree.Id, tree); err != nil {
			return fmt.Errorf("failed to set tree %d: %w", tree.Id, err)
		}
	}
	for _, asset := range genState.Decompressed {
		if err := k.Decompressed.Set(ctx, asset.AssetId, asset); err != nil {
			return fmt.Errorf("failed to set decompressed asset: %w", err)
		}
	}
	nextSnap := make(map[uint64]uint64, len(genState.Snapshots))
	for _, snap := range genState.Snapshots {
		if err := k.Snapshots.Set(ctx, collections.Join(snap.TreeId, snap.Id), snap); err != nil {
			return fmt.Errorf("failed to set snapshot %d: %w", snap.Id, err)
		}
		if snap.Id+1 > nextSnap[snap.TreeId] {
			nextSnap[snap.TreeId] = snap.Id + 1
		}
	}
	for treeID, next := range nextSnap {
		if err := k.NextSnapshot.Set(ctx, treeID, next); err != nil {
			return fmt.Errorf("failed to set next snapshot id for tree %d: %w", treeID, err)
		}
	}
	return nil
}

// ExportGenesis reads x/cnft's current state.
func (k Keeper) ExportGenesis(ctx sdk.Context) (*types.GenesisState, error) {
	nextCollection, err := k.NextCollectionID.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get next collection id: %w", err)
	}
	nextTree, err := k.NextTreeID.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get next tree id: %w", err)
	}
	var collectionsOut []types.Collection
	if err := k.Collections.Walk(ctx, nil, func(_ uint64, col types.Collection) (bool, error) {
		collectionsOut = append(collectionsOut, col)
		return false, nil
	}); err != nil {
		return nil, fmt.Errorf("failed to walk collections: %w", err)
	}
	var trees []types.Tree
	if err := k.Trees.Walk(ctx, nil, func(_ uint64, tree types.Tree) (bool, error) {
		trees = append(trees, tree)
		return false, nil
	}); err != nil {
		return nil, fmt.Errorf("failed to walk trees: %w", err)
	}
	var decompressed []types.DecompressedAsset
	if err := k.Decompressed.Walk(ctx, nil, func(_ []byte, asset types.DecompressedAsset) (bool, error) {
		decompressed = append(decompressed, asset)
		return false, nil
	}); err != nil {
		return nil, fmt.Errorf("failed to walk decompressed assets: %w", err)
	}
	var snapshots []types.Snapshot
	if err := k.Snapshots.Walk(ctx, nil, func(_ collections.Pair[uint64, uint64], snap types.Snapshot) (bool, error) {
		snapshots = append(snapshots, snap)
		return false, nil
	}); err != nil {
		return nil, fmt.Errorf("failed to walk snapshots: %w", err)
	}
	if collectionsOut == nil {
		collectionsOut = []types.Collection{}
	}
	if trees == nil {
		trees = []types.Tree{}
	}
	if decompressed == nil {
		decompressed = []types.DecompressedAsset{}
	}
	if snapshots == nil {
		snapshots = []types.Snapshot{}
	}
	return &types.GenesisState{
		Collections:      collectionsOut,
		Trees:            trees,
		Decompressed:     decompressed,
		Snapshots:        snapshots,
		NextCollectionId: nextCollection,
		NextTreeId:       nextTree,
	}, nil
}
