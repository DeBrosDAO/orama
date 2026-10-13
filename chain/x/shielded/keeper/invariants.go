package keeper

import (
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/shielded/bundle"
	"github.com/DeBrosOfficial/network/chain/x/shielded/nullifier"
	"github.com/DeBrosOfficial/network/chain/x/shielded/types"
)

// Invariants is the result of CheckInvariants.
type Invariants struct {
	BalanceMatches     bool
	PoolsNonNegative   bool
	AccumulatorMatches bool
	// SubtreeRootsMatch is whether a root is stored for every filled 2^16-leaf subtree and none
	// beyond: floor(tree_size / 65536) of them.
	SubtreeRootsMatch bool
	// QueueIndexMatches is whether the per-owner count of queued unshields equals the queue's rows.
	QueueIndexMatches bool
	Detail            string
}

// CheckInvariants checks the module's invariants (plans/open-network/track-c-chain.md C12):
//
//   - the module account balance == the sum of pool balances + the queued unshields
//   - no pool balance is negative, no queued amount is not positive
//   - the per-owner count of queued unshields is the number of queue rows each owner has
//   - the nullifier database folds, in insertion order, to the accumulator in state
//   - there is a subtree root for each filled subtree of the tree and none beyond: the last stored
//     index is floor(tree_size / 65536) - 1
func (k Keeper) CheckInvariants(ctx sdk.Context) (Invariants, error) {
	inv, err := k.CheckLedgers(ctx)
	if err != nil {
		return inv, err
	}
	if err := k.checkAccumulator(ctx, &inv); err != nil {
		return inv, err
	}
	return inv, nil
}

// CheckLedgers is CheckInvariants without the nullifier-database fold: the module account against
// the pools and the queue, the signs of both, and the subtree roots against the tree's size. It
// reads only committed state, so the chain can run it inside a block; the fold reads a database
// outside the state and costs O(nullifiers).
func (k Keeper) CheckLedgers(ctx sdk.Context) (Invariants, error) {
	inv := Invariants{BalanceMatches: true, PoolsNonNegative: true, AccumulatorMatches: true, SubtreeRootsMatch: true, QueueIndexMatches: true}
	held := math.ZeroInt()
	err := k.Pools.Walk(ctx, nil, func(key collections.Pair[uint32, []byte], bal math.Int) (bool, error) {
		if bal.IsNegative() {
			inv.PoolsNonNegative = false
			inv.Detail += fmt.Sprintf("pool %d is negative (%s); ", key.K1(), bal)
		}
		held = held.Add(bal)
		return false, nil
	})
	if err != nil {
		return inv, fmt.Errorf("walk pools: %w", err)
	}
	queue, err := k.queued(ctx)
	if err != nil {
		return inv, fmt.Errorf("walk the queue: %w", err)
	}
	rows := map[string]uint64{}
	for _, q := range queue {
		rows[q.Owner]++
		if !q.Amount.IsPositive() {
			inv.PoolsNonNegative = false
			inv.Detail += fmt.Sprintf("queued unshield %d has amount %s; ", q.Id, q.Amount)
		}
		held = held.Add(q.Amount)
	}
	if err := k.checkQueueIndex(ctx, rows, &inv); err != nil {
		return inv, err
	}
	module := k.deps.Bank.GetBalance(ctx, authtypes.NewModuleAddress(types.ModuleName), params.BaseDenom).Amount
	if !module.Equal(held) {
		inv.BalanceMatches = false
		inv.Detail += fmt.Sprintf("module holds %s but pools and queue account for %s; ", module, held)
	}
	if err := k.checkSubtreeRoots(ctx, &inv); err != nil {
		return inv, err
	}
	return inv, nil
}

// checkQueueIndex compares the per-owner counts with the rows the queue walk found.
func (k Keeper) checkQueueIndex(ctx sdk.Context, rows map[string]uint64, inv *Invariants) error {
	seen := 0
	err := k.QueueOwners.Walk(ctx, nil, func(owner string, n uint64) (bool, error) {
		seen++
		if rows[owner] != n {
			inv.QueueIndexMatches = false
			inv.Detail += fmt.Sprintf("%s has %d queued unshields but the index counts %d; ", owner, rows[owner], n)
		}
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("walk the queue owner index: %w", err)
	}
	if seen != len(rows) {
		inv.QueueIndexMatches = false
		inv.Detail += fmt.Sprintf("the queue has %d owners but the index %d; ", len(rows), seen)
	}
	return nil
}

// checkSubtreeRoots looks at the two ends of the stored roots, which is all an append-only run
// without gaps needs: the root of the last filled subtree is there and the next one is not. Genesis
// import checks the whole run (GenesisState.Validate), and every later root is stored in order.
func (k Keeper) checkSubtreeRoots(ctx sdk.Context, inv *Invariants) error {
	size, err := getUint64(ctx, k.TreeSize)
	if err != nil {
		return fmt.Errorf("read the tree size: %w", err)
	}
	filled := size / types.SubtreeLeaves
	hasNext, err := k.Subtrees.Has(ctx, filled)
	if err != nil {
		return fmt.Errorf("look for subtree root %d: %w", filled, err)
	}
	hasLast := filled == 0
	if filled > 0 {
		if hasLast, err = k.Subtrees.Has(ctx, filled-1); err != nil {
			return fmt.Errorf("look for subtree root %d: %w", filled-1, err)
		}
	}
	if hasNext || !hasLast {
		inv.SubtreeRootsMatch = false
		inv.Detail += fmt.Sprintf("tree of %d leaves has filled %d subtrees (last root present %t, root beyond it present %t); ", size, filled, hasLast, hasNext)
	}
	return nil
}

func (k Keeper) checkAccumulator(ctx sdk.Context, inv *Invariants) error {
	want, err := k.accumulator(ctx)
	if err != nil {
		return err
	}
	count, err := getUint64(ctx, k.NullifierCount)
	if err != nil {
		return fmt.Errorf("read nullifier count: %w", err)
	}
	var got [bundle.NodeLen]byte
	var seen uint64
	err = k.deps.Nullifiers.Walk(func(nf [bundle.NodeLen]byte, _ int64) error {
		if seen == count {
			return errStopWalk
		}
		got = nullifier.Fold(got, nf)
		seen++
		return nil
	})
	if err != nil && !errors.Is(err, errStopWalk) {
		return fmt.Errorf("%w: walk nullifiers: %w", types.ErrNullifierStore, err)
	}
	if seen != count || got != want {
		inv.AccumulatorMatches = false
		inv.Detail += fmt.Sprintf("nullifier database has %d records folding to %x; state has %d committing to %x; ", seen, got, count, want)
	}
	return nil
}

// CheckNullifierStore verifies that the nullifier database folds, in insertion order, to the
// accumulator and count the committed state holds. A node runs it at start: a database that
// disagrees (missing after a restore, from another chain, cut short) would accept a spent
// nullifier or refuse a fresh one, and the node would diverge on the first shielded bundle.
func (k Keeper) CheckNullifierStore(ctx sdk.Context) error {
	inv := Invariants{AccumulatorMatches: true}
	if err := k.checkAccumulator(ctx, &inv); err != nil {
		return err
	}
	if !inv.AccumulatorMatches {
		return fmt.Errorf("%w: %s", types.ErrNullifierStore, inv.Detail)
	}
	return nil
}
