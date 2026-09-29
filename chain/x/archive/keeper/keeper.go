// Package keeper implements x/archive: a registry of height ranges to a bundle
// CID, a block-hash Merkle root and replica deal ids
// (plans/open-network/track-c-chain.md C14).
//
// The deals themselves stay in x/storage. This keeper only records deal ids and
// does not import x/storage. There is no admin key and no authority address.
package keeper

import (
	"bytes"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	storetypes "cosmossdk.io/core/store"

	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/archive/types"
)

// Keeper is x/archive's keeper.
type Keeper struct {
	Schema             collections.Schema
	Params             collections.Item[types.Params]
	LastArchivedHeight collections.Item[int64]
	Ranges             collections.Map[collections.Pair[int64, int64], types.RangeRecord]

	nodes   types.NodesKeeper
	storage types.StorageKeeper
}

// NewKeeper builds an x/archive Keeper.
func NewKeeper(cdc codec.BinaryCodec, storeService storetypes.KVStoreService, nodes types.NodesKeeper, storage types.StorageKeeper) Keeper {
	sb := collections.NewSchemaBuilder(storeService)
	k := Keeper{
		nodes:              nodes,
		storage:            storage,
		Params:             collections.NewItem(sb, types.ParamsKey, "params", codec.CollValue[types.Params](cdc)),
		LastArchivedHeight: collections.NewItem(sb, types.LastArchivedHeightKey, "last_archived_height", collections.Int64Value),
		Ranges: collections.NewMap(
			sb,
			types.RangesPrefix,
			"ranges",
			collections.PairKeyCodec(collections.Int64Key, collections.Int64Key),
			codec.CollValue[types.RangeRecord](cdc),
		),
	}
	schema, err := sb.Build()
	if err != nil {
		panic(err)
	}
	k.Schema = schema
	return k
}

// RetainHeight is the CometBFT retain height at the current block: blocks
// strictly below it may be pruned, and it is never above the last archived height.
func (k Keeper) RetainHeight(ctx sdk.Context) (int64, error) {
	blocks, last, err := k.retentionInputs(ctx)
	if err != nil {
		return 0, err
	}
	return types.RetainHeight(ctx.BlockHeight(), blocks, last), nil
}

// PruneAllowed reports whether the block at height may be deleted.
// A request to prune at or past the retain height is refused.
func (k Keeper) PruneAllowed(ctx sdk.Context, height int64) (bool, error) {
	blocks, last, err := k.retentionInputs(ctx)
	if err != nil {
		return false, err
	}
	return types.PruneAllowed(height, ctx.BlockHeight(), blocks, last), nil
}

func (k Keeper) retentionInputs(ctx sdk.Context) (int64, int64, error) {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to get archive params: %w", err)
	}
	last, err := k.LastArchivedHeight.Get(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to get last archived height: %w", err)
	}
	return params.RetentionWindowBlocks, last, nil
}

// VerifyBundle loads an archived range and checks blockHashes and bundleHash
// against the pinned root and content hash. A mutated header fails.
func (k Keeper) VerifyBundle(ctx sdk.Context, start, end int64, blockHashes [][]byte, bundleHash []byte) error {
	rec, err := k.GetRange(ctx, start, end)
	if err != nil {
		return err
	}
	if !rec.Archived {
		return fmt.Errorf("%w: %d-%d", types.ErrNotArchived, start, end)
	}
	if !bytes.Equal(bundleHash, rec.BundleHash) {
		return fmt.Errorf("%w: bundle hash mismatch for %d-%d", types.ErrWrongBundle, start, end)
	}
	span := uint64(end) - uint64(start) + 1
	if uint64(len(blockHashes)) != span {
		return fmt.Errorf("got %d block hashes for range %d-%d (%d blocks)", len(blockHashes), start, end, span)
	}
	if err := types.VerifyBundle(blockHashes, bundleHash, rec.MerkleRoot); err != nil {
		return fmt.Errorf("range %d-%d: %w", start, end, err)
	}
	return nil
}

// GetRange returns one range record.
func (k Keeper) GetRange(ctx sdk.Context, start, end int64) (types.RangeRecord, error) {
	rec, err := k.Ranges.Get(ctx, collections.Join(start, end))
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return types.RangeRecord{}, fmt.Errorf("%w: %d-%d", types.ErrUnknownRange, start, end)
		}
		return types.RangeRecord{}, fmt.Errorf("failed to get range %d-%d: %w", start, end, err)
	}
	return rec, nil
}
