package keeper

import (
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/shielded/bundle"
	"github.com/DeBrosOfficial/network/chain/x/shielded/nullifier"
	"github.com/DeBrosOfficial/network/chain/x/shielded/types"
)

// InitGenesis sets the module's state from genesis. The nullifier database must be empty: a
// database left from another run belongs to another chain, and starting from genesis on it would
// refuse bundles the new chain has never seen. `oramad tendermint unsafe-reset-all` removes it
// with the rest of data/.
func (k Keeper) InitGenesis(ctx sdk.Context, gs types.GenesisState) error {
	if err := gs.Validate(); err != nil {
		return fmt.Errorf("invalid shielded genesis state: %w", err)
	}
	empty, err := k.deps.Nullifiers.Empty()
	if err != nil {
		return fmt.Errorf("%w: %w", types.ErrNullifierStore, err)
	}
	if !empty {
		return fmt.Errorf("%w: the nullifier database is not empty; it belongs to another chain", types.ErrNullifierStore)
	}
	if err := k.Params.Set(ctx, gs.Params); err != nil {
		return fmt.Errorf("set shielded params: %w", err)
	}
	if err := k.initPools(ctx, gs); err != nil {
		return err
	}
	if err := k.initTree(ctx, gs); err != nil {
		return err
	}
	return k.initNullifiers(ctx, gs)
}

func (k Keeper) initPools(ctx context.Context, gs types.GenesisState) error {
	for _, p := range gs.Pools {
		if err := k.Pools.Set(ctx, collections.Join(p.Vintage, p.Asset), p.Balance); err != nil {
			return fmt.Errorf("set pool: %w", err)
		}
	}
	for _, l := range gs.Limiters {
		if err := k.Limiters.Set(ctx, collections.Join(l.Vintage, l.Asset), l); err != nil {
			return fmt.Errorf("set limiter: %w", err)
		}
	}
	for _, q := range gs.Queue {
		if err := k.Queue.Set(ctx, q.Id, q); err != nil {
			return fmt.Errorf("set queued unshield: %w", err)
		}
	}
	return k.NextQueueID.Set(ctx, gs.NextQueueId)
}

func (k Keeper) initTree(ctx context.Context, gs types.GenesisState) error {
	if len(gs.Frontier) > 0 {
		if err := k.Frontier.Set(ctx, gs.Frontier); err != nil {
			return fmt.Errorf("set frontier: %w", err)
		}
		if err := k.CurrentRoot.Set(ctx, gs.CurrentRoot); err != nil {
			return fmt.Errorf("set tree root: %w", err)
		}
		if err := k.TreeSize.Set(ctx, gs.TreeSize); err != nil {
			return fmt.Errorf("set tree size: %w", err)
		}
	}
	for _, a := range gs.Anchors {
		if err := k.Anchors.Set(ctx, a.Root, a.Height); err != nil {
			return fmt.Errorf("set anchor: %w", err)
		}
		if err := k.AnchorHeights.Set(ctx, a.Height, a.Root); err != nil {
			return fmt.Errorf("set anchor height: %w", err)
		}
	}
	return nil
}

// importChunk bounds one write batch of the nullifier import.
const importChunk = 10_000

// initNullifiers writes the accumulator to state and the records to the database, in the order the
// accumulator folded them, all at height 0 (nullifier.Store.Import).
func (k Keeper) initNullifiers(ctx context.Context, gs types.GenesisState) error {
	if err := k.Accumulator.Set(ctx, gs.NullifierAccumulator); err != nil {
		return fmt.Errorf("set nullifier accumulator: %w", err)
	}
	if err := k.NullifierCount.Set(ctx, gs.NullifierCount); err != nil {
		return fmt.Errorf("set nullifier count: %w", err)
	}
	for start := 0; start < len(gs.Nullifiers); start += importChunk {
		end := min(start+importChunk, len(gs.Nullifiers))
		chunk := make([][bundle.NodeLen]byte, 0, end-start)
		for _, raw := range gs.Nullifiers[start:end] {
			var nf [bundle.NodeLen]byte
			copy(nf[:], raw)
			chunk = append(chunk, nf)
		}
		if err := k.deps.Nullifiers.Import(chunk); err != nil {
			return fmt.Errorf("%w: import nullifiers: %w", types.ErrNullifierStore, err)
		}
	}
	return nil
}

// ExportGenesis reads the module's state back. The nullifier list is the first
// nullifier_count records of the database: records past that belong to a block that was executed
// but never committed.
func (k Keeper) ExportGenesis(ctx sdk.Context) (*types.GenesisState, error) {
	gs := types.DefaultGenesisState()
	var err error
	if gs.Params, err = k.params(ctx); err != nil {
		return nil, err
	}
	if err := k.exportPools(ctx, gs); err != nil {
		return nil, err
	}
	if err := k.exportTree(ctx, gs); err != nil {
		return nil, err
	}
	if err := k.exportNullifiers(ctx, gs); err != nil {
		return nil, err
	}
	return gs, nil
}

func (k Keeper) exportPools(ctx context.Context, gs *types.GenesisState) error {
	err := k.Pools.Walk(ctx, nil, func(key collections.Pair[uint32, []byte], bal math.Int) (bool, error) {
		gs.Pools = append(gs.Pools, types.PoolBalance{Vintage: key.K1(), Asset: key.K2(), Balance: bal})
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("export pools: %w", err)
	}
	err = k.Limiters.Walk(ctx, nil, func(_ collections.Pair[uint32, []byte], l types.Limiter) (bool, error) {
		gs.Limiters = append(gs.Limiters, l)
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("export limiters: %w", err)
	}
	if gs.Queue, err = k.queued(ctx); err != nil {
		return fmt.Errorf("export the queue: %w", err)
	}
	if gs.NextQueueId, err = k.NextQueueID.Peek(ctx); err != nil {
		return fmt.Errorf("export next queue id: %w", err)
	}
	return nil
}

func (k Keeper) exportTree(ctx context.Context, gs *types.GenesisState) error {
	var err error
	if gs.Frontier, err = getBytes(ctx, k.Frontier); err != nil {
		return fmt.Errorf("export frontier: %w", err)
	}
	if gs.CurrentRoot, err = getBytes(ctx, k.CurrentRoot); err != nil {
		return fmt.Errorf("export tree root: %w", err)
	}
	if gs.TreeSize, err = getUint64(ctx, k.TreeSize); err != nil {
		return fmt.Errorf("export tree size: %w", err)
	}
	err = k.Anchors.Walk(ctx, nil, func(root []byte, height int64) (bool, error) {
		gs.Anchors = append(gs.Anchors, types.Anchor{Root: root, Height: height})
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("export anchors: %w", err)
	}
	return nil
}

func (k Keeper) exportNullifiers(ctx context.Context, gs *types.GenesisState) error {
	acc, err := k.accumulator(ctx)
	if err != nil {
		return err
	}
	count, err := getUint64(ctx, k.NullifierCount)
	if err != nil {
		return fmt.Errorf("export nullifier count: %w", err)
	}
	gs.NullifierAccumulator, gs.NullifierCount = acc[:], count
	err = k.deps.Nullifiers.Walk(func(nf [bundle.NodeLen]byte, _ int64) error {
		if uint64(len(gs.Nullifiers)) == count {
			return errStopWalk
		}
		gs.Nullifiers = append(gs.Nullifiers, nf[:])
		return nil
	})
	if err != nil && !errors.Is(err, errStopWalk) {
		return fmt.Errorf("%w: export nullifiers: %w", types.ErrNullifierStore, err)
	}
	if uint64(len(gs.Nullifiers)) != count {
		return fmt.Errorf("%w: state counts %d nullifiers, the database holds %d", types.ErrNullifierStore, count, len(gs.Nullifiers))
	}
	var folded [bundle.NodeLen]byte
	for _, raw := range gs.Nullifiers {
		var nf [bundle.NodeLen]byte
		copy(nf[:], raw)
		folded = nullifier.Fold(folded, nf)
	}
	if folded != acc {
		return fmt.Errorf("%w: the database folds to %x, state commits to %x", types.ErrNullifierStore, folded, acc)
	}
	return nil
}
