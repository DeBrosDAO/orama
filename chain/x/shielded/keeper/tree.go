package keeper

import (
	"context"
	"fmt"

	"github.com/DeBrosOfficial/network/chain/x/shielded/bundle"
	"github.com/DeBrosOfficial/network/chain/x/shielded/nullifier"
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

func (k Keeper) appendCommitments(ctx context.Context, cmx [][bundle.NodeLen]byte) error {
	frontier, err := getBytes(ctx, k.Frontier)
	if err != nil {
		return fmt.Errorf("read the tree frontier: %w", err)
	}
	next, root, err := k.deps.Tree.Append(frontier, cmx)
	if err != nil {
		return fmt.Errorf("append %d commitments to the tree: %w", len(cmx), err)
	}
	size, err := getUint64(ctx, k.TreeSize)
	if err != nil {
		return fmt.Errorf("read the tree size: %w", err)
	}
	if err := k.Frontier.Set(ctx, next); err != nil {
		return fmt.Errorf("write the tree frontier: %w", err)
	}
	if err := k.CurrentRoot.Set(ctx, root[:]); err != nil {
		return fmt.Errorf("write the tree root: %w", err)
	}
	if err := k.TreeSize.Set(ctx, size+uint64(len(cmx))); err != nil {
		return fmt.Errorf("write the tree size: %w", err)
	}
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
