// Package keeper implements x/cnft: compressed-NFT trees, their changelog buffer,
// and the on-chain handle used by decompress and compress.
package keeper

import (
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	storetypes "cosmossdk.io/core/store"
	"cosmossdk.io/log/v2"

	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/cnft/types"
)

// Keeper is x/cnft's keeper. It has no authority address: collections, trees and
// leaves are controlled by their creators and owners (plans/open-network.md D18).
type Keeper struct {
	storeService storetypes.KVStoreService
	fees         types.FeesKeeper
	// earnings is wired so a transfer can be shown not to credit a royalty.
	// No method here calls it. Sale proceeds are credited by x/market.
	earnings types.EarningsKeeper

	Schema           collections.Schema
	NextCollectionID collections.Item[uint64]
	NextTreeID       collections.Item[uint64]
	Collections      collections.Map[uint64, types.Collection]
	Trees            collections.Map[uint64, types.Tree]
	Decompressed     collections.Map[[]byte, types.DecompressedAsset]
	Snapshots        collections.Map[collections.Pair[uint64, uint64], types.Snapshot]
	NextSnapshot     collections.Map[uint64, uint64]
}

// NewKeeper builds an x/cnft keeper.
func NewKeeper(
	cdc codec.BinaryCodec,
	storeService storetypes.KVStoreService,
	fees types.FeesKeeper,
	earnings types.EarningsKeeper,
) Keeper {
	sb := collections.NewSchemaBuilder(storeService)
	k := Keeper{
		storeService:     storeService,
		fees:             fees,
		earnings:         earnings,
		NextCollectionID: collections.NewItem(sb, types.NextCollectionIDKey, "next_collection_id", collections.Uint64Value),
		NextTreeID:       collections.NewItem(sb, types.NextTreeIDKey, "next_tree_id", collections.Uint64Value),
		Collections:      collections.NewMap(sb, types.CollectionsPrefix, "collections", collections.Uint64Key, codec.CollValue[types.Collection](cdc)),
		Trees:            collections.NewMap(sb, types.TreesPrefix, "trees", collections.Uint64Key, codec.CollValue[types.Tree](cdc)),
		Decompressed:     collections.NewMap(sb, types.DecompressedPrefix, "decompressed", collections.BytesKey, codec.CollValue[types.DecompressedAsset](cdc)),
		Snapshots: collections.NewMap(
			sb,
			types.SnapshotsPrefix,
			"snapshots",
			collections.PairKeyCodec(collections.Uint64Key, collections.Uint64Key),
			codec.CollValue[types.Snapshot](cdc),
		),
		NextSnapshot: collections.NewMap(sb, types.NextSnapshotPrefix, "next_snapshot", collections.Uint64Key, collections.Uint64Value),
	}
	schema, err := sb.Build()
	if err != nil {
		panic(err)
	}
	k.Schema = schema
	return k
}

// Earnings returns the earnings keeper passed to NewKeeper.
func (k Keeper) Earnings() types.EarningsKeeper { return k.earnings }

// Logger returns a module-specific logger.
func (k Keeper) Logger(ctx context.Context) log.Logger {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	return sdkCtx.Logger().With("module", "x/"+types.ModuleName)
}

func (k Keeper) takeID(ctx context.Context, item collections.Item[uint64], name string) (uint64, error) {
	id, err := item.Get(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to load next %s id: %w", name, err)
	}
	if err := item.Set(ctx, id+1); err != nil {
		return 0, fmt.Errorf("failed to store next %s id: %w", name, err)
	}
	return id, nil
}

func (k Keeper) loadTree(ctx context.Context, id uint64) (types.Tree, error) {
	tree, err := k.Trees.Get(ctx, id)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return types.Tree{}, fmt.Errorf("tree %d does not exist", id)
		}
		return types.Tree{}, fmt.Errorf("failed to load tree %d: %w", id, err)
	}
	return tree, nil
}

func (k Keeper) loadCollection(ctx context.Context, id uint64) (types.Collection, error) {
	col, err := k.Collections.Get(ctx, id)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return types.Collection{}, fmt.Errorf("collection %d does not exist", id)
		}
		return types.Collection{}, fmt.Errorf("failed to load collection %d: %w", id, err)
	}
	return col, nil
}

func (k Keeper) storeTree(ctx context.Context, tree types.Tree) error {
	if err := k.Trees.Set(ctx, tree.Id, tree); err != nil {
		return fmt.Errorf("failed to store tree %d: %w", tree.Id, err)
	}
	return nil
}

// GetTree returns a tree by id.
func (k Keeper) GetTree(ctx context.Context, id uint64) (types.Tree, error) {
	return k.loadTree(ctx, id)
}

// GetCollection returns a collection by id.
func (k Keeper) GetCollection(ctx context.Context, id uint64) (types.Collection, error) {
	return k.loadCollection(ctx, id)
}
