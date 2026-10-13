package keeper

import (
	"context"
	"encoding/hex"
	"fmt"
	"strconv"

	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/shielded/bundle"
	"github.com/DeBrosOfficial/network/chain/x/shielded/nullifier"
	"github.com/DeBrosOfficial/network/chain/x/shielded/types"
)

// register makes a bundle's effects on the tree and the nullifier set: it folds each nullifier
// into the running accumulator (which is in IAVL, so the app hash commits to it), marks it pending
// for the end-of-block flush to the nullifier database, and appends the note commitments.
func (k Keeper) register(ctx context.Context, b *bundle.Bundle) error {
	acc, err := k.accumulator(ctx)
	if err != nil {
		return err
	}
	count, err := getUint64(ctx, k.NullifierCount)
	if err != nil {
		return fmt.Errorf("read nullifier count: %w", err)
	}
	for _, nf := range b.Nullifiers {
		acc = nullifier.Fold(acc, nf)
	}
	if err := k.Accumulator.Set(ctx, acc[:]); err != nil {
		return fmt.Errorf("write nullifier accumulator: %w", err)
	}
	if err := k.NullifierCount.Set(ctx, count+uint64(len(b.Nullifiers))); err != nil {
		return fmt.Errorf("write nullifier count: %w", err)
	}
	if err := k.MarkPending(ctx, b.Nullifiers); err != nil {
		return err
	}
	return k.appendCommitments(ctx, b.Commitments)
}

func (k Keeper) accumulator(ctx context.Context) ([bundle.NodeLen]byte, error) {
	var acc [bundle.NodeLen]byte
	raw, err := getBytes(ctx, k.Accumulator)
	if err != nil {
		return acc, fmt.Errorf("read nullifier accumulator: %w", err)
	}
	copy(acc[:], raw)
	return acc, nil
}

// appendCommitments appends the commitments to the tree and, for every subtree they fill, stores
// and emits the subtree's root. The append is split where a subtree fills, so the frontier that closes it is
// at hand: after the next leaf the subtree's root is folded into a higher ommer and gone.
func (k Keeper) appendCommitments(ctx context.Context, cmx [][bundle.NodeLen]byte) error {
	if len(cmx) == 0 {
		return nil
	}
	frontier, err := getBytes(ctx, k.Frontier)
	if err != nil {
		return fmt.Errorf("read the tree frontier: %w", err)
	}
	size, err := getUint64(ctx, k.TreeSize)
	if err != nil {
		return fmt.Errorf("read the tree size: %w", err)
	}
	var root [bundle.NodeLen]byte
	for len(cmx) > 0 {
		chunk := min(uint64(len(cmx)), types.SubtreeLeaves-size%types.SubtreeLeaves)
		if frontier, root, err = k.deps.Tree.Append(frontier, cmx[:chunk]); err != nil {
			return fmt.Errorf("append %d commitments to the tree: %w", chunk, err)
		}
		cmx = cmx[chunk:]
		size += chunk
		if size%types.SubtreeLeaves == 0 {
			if err := k.emitSubtree(ctx, frontier, size/types.SubtreeLeaves-1); err != nil {
				return err
			}
		}
	}
	if err := k.Frontier.Set(ctx, frontier); err != nil {
		return fmt.Errorf("write the tree frontier: %w", err)
	}
	if err := k.CurrentRoot.Set(ctx, root[:]); err != nil {
		return fmt.Errorf("write the tree root: %w", err)
	}
	if err := k.TreeSize.Set(ctx, size); err != nil {
		return fmt.Errorf("write the tree size: %w", err)
	}
	return nil
}

// emitSubtree stores the root of subtree index, which the frontier has just closed, with the
// height that filled it, and reports it in an event.
func (k Keeper) emitSubtree(ctx context.Context, frontier []byte, index uint64) error {
	root, err := k.deps.Tree.SubtreeRoot(frontier)
	if err != nil {
		return fmt.Errorf("compute the root of subtree %d: %w", index, err)
	}
	// The boundary tx does one write and one event more than the tx before it, so it is charged
	// on an unmetered context: a gas limit estimated from the tx before it still covers it.
	sctx := sdk.UnwrapSDKContext(ctx).WithGasMeter(storetypes.NewInfiniteGasMeter())
	if err := k.Subtrees.Set(sctx, index, types.SubtreeRoot{Index: index, Root: root[:], Height: sctx.BlockHeight()}); err != nil {
		return fmt.Errorf("store the root of subtree %d: %w", index, err)
	}
	sctx.EventManager().EmitEvent(sdk.NewEvent(types.EventTypeSubtreeComplete,
		sdk.NewAttribute(types.AttributeSubtreeIndex, strconv.FormatUint(index, 10)),
		sdk.NewAttribute(types.AttributeSubtreeRoot, hex.EncodeToString(root[:])),
	))
	return nil
}

// NullifierState is the accumulator and count the module state commits to.
func (k Keeper) NullifierState(ctx context.Context) ([bundle.NodeLen]byte, uint64, error) {
	acc, err := k.accumulator(ctx)
	if err != nil {
		return acc, 0, err
	}
	count, err := getUint64(ctx, k.NullifierCount)
	if err != nil {
		return acc, 0, fmt.Errorf("read nullifier count: %w", err)
	}
	return acc, count, nil
}
