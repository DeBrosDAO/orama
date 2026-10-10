package keeper

import (
	"context"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/shielded/bundle"
	"github.com/DeBrosOfficial/network/chain/x/shielded/types"
)

// asOf is the height a nullifier read is made at: a record is spent when it was written at a
// height below it. A block reads at its own height, so it never sees records it (or a block it
// replaces) wrote; the mempool's check state reads one past the last committed block.
func asOf(ctx context.Context) int64 {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	if sdkCtx.IsCheckTx() || sdkCtx.IsReCheckTx() {
		return sdkCtx.BlockHeight() + 1
	}
	return sdkCtx.BlockHeight()
}

// checkUnspent refuses a nullifier that is in the database or already pending in this check
// state or block.
func (k Keeper) checkUnspent(ctx context.Context, nf [bundle.NodeLen]byte) error {
	spent, err := k.deps.Nullifiers.Spent(nf, asOf(ctx))
	if err != nil {
		return fmt.Errorf("%w: %w", types.ErrNullifierStore, err)
	}
	if spent {
		return fmt.Errorf("%w: %x", types.ErrNullifierSpent, nf)
	}
	pending, err := k.pendingSet.Has(ctx, nf[:])
	if err != nil {
		return fmt.Errorf("read pending nullifiers: %w", err)
	}
	if pending {
		return fmt.Errorf("%w: %x is pending", types.ErrNullifierSpent, nf)
	}
	return nil
}

// MarkPending records a bundle's nullifiers as pending. In the mempool's check state this is
// what stops a second bundle with the same nullifier; in a block it is the list the end-of-block
// flush writes to the nullifier database. It lives in a transient store, so a failed tx rolls it
// back with everything else and Commit clears it.
func (k Keeper) MarkPending(ctx context.Context, nfs [][bundle.NodeLen]byte) error {
	for _, nf := range nfs {
		if err := k.pendingSet.Set(ctx, nf[:]); err != nil {
			return fmt.Errorf("mark nullifier pending: %w", err)
		}
		seq, err := k.pendingSeq.Next(ctx)
		if err != nil {
			return fmt.Errorf("next pending sequence: %w", err)
		}
		if err := k.pendingList.Set(ctx, seq, nf[:]); err != nil {
			return fmt.Errorf("record pending nullifier: %w", err)
		}
	}
	return nil
}

// pendingInOrder returns the block's pending nullifiers in the order they were marked.
func (k Keeper) pendingInOrder(ctx context.Context) ([][bundle.NodeLen]byte, error) {
	var out [][bundle.NodeLen]byte
	err := k.pendingList.Walk(ctx, nil, func(_ uint64, raw []byte) (bool, error) {
		var nf [bundle.NodeLen]byte
		if copy(nf[:], raw) != bundle.NodeLen {
			return true, fmt.Errorf("%w: pending nullifier is %d bytes", types.ErrNullifierStore, len(raw))
		}
		out = append(out, nf)
		return false, nil
	})
	return out, err
}
