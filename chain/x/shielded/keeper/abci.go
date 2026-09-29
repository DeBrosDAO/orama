package keeper

import (
	"errors"
	"fmt"

	"cosmossdk.io/collections"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// EndBlock pays the unshield queue when a window is due, writes the block's nullifiers to the
// nullifier database and records the tree root as an anchor. It runs before x/staking's end
// blocker so a bond the queue pays is seen by that block's validator set.
//
// The database write happens here, before Commit. If the node stops between the two, the block is
// replayed, and Commit (nullifier.Store) drops the records this attempt left so the replay
// writes its own.
func (k Keeper) EndBlock(ctx sdk.Context) error {
	k.admission.newBlock()
	if err := k.serveQueue(ctx); err != nil {
		return fmt.Errorf("serve the unshield queue: %w", err)
	}
	nfs, err := k.pendingInOrder(ctx)
	if err != nil {
		return err
	}
	if err := k.deps.Nullifiers.Commit(ctx.BlockHeight(), nfs); err != nil {
		return fmt.Errorf("write the block's nullifiers: %w", err)
	}
	return k.recordAnchor(ctx)
}

// recordAnchor makes the current root an anchor for this block and drops the anchor that just left
// the window. A root that is still the current root at a later block is recorded again, so an idle
// tree's root never expires.
func (k Keeper) recordAnchor(ctx sdk.Context) error {
	root, err := getBytes(ctx, k.CurrentRoot)
	if err != nil {
		return fmt.Errorf("read the tree root: %w", err)
	}
	if len(root) == 0 {
		return nil
	}
	p, err := k.params(ctx)
	if err != nil {
		return err
	}
	height := ctx.BlockHeight()
	if err := k.Anchors.Set(ctx, root, height); err != nil {
		return fmt.Errorf("record anchor: %w", err)
	}
	if err := k.AnchorHeights.Set(ctx, height, root); err != nil {
		return fmt.Errorf("record anchor height: %w", err)
	}
	expired := height - int64(p.AnchorWindowBlocks) - 1
	old, err := k.AnchorHeights.Get(ctx, expired)
	if errors.Is(err, collections.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read expiring anchor: %w", err)
	}
	if err := k.AnchorHeights.Remove(ctx, expired); err != nil {
		return fmt.Errorf("drop expired anchor height: %w", err)
	}
	if at, err := k.Anchors.Get(ctx, old); err == nil && at == expired {
		return k.Anchors.Remove(ctx, old)
	}
	return nil
}
