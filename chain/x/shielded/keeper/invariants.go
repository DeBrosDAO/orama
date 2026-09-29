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
	Detail             string
}

// CheckInvariants checks the module's invariants (plans/open-network/track-c-chain.md C12):
//
//   - the module account balance == the sum of pool balances + the queued unshields
//   - no pool balance is negative, no queued amount is not positive
//   - the nullifier database folds, in insertion order, to the accumulator in state
func (k Keeper) CheckInvariants(ctx sdk.Context) (Invariants, error) {
	inv := Invariants{BalanceMatches: true, PoolsNonNegative: true, AccumulatorMatches: true}
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
	for _, q := range queue {
		if !q.Amount.IsPositive() {
			inv.PoolsNonNegative = false
			inv.Detail += fmt.Sprintf("queued unshield %d has amount %s; ", q.Id, q.Amount)
		}
		held = held.Add(q.Amount)
	}
	module := k.deps.Bank.GetBalance(ctx, authtypes.NewModuleAddress(types.ModuleName), params.BaseDenom).Amount
	if !module.Equal(held) {
		inv.BalanceMatches = false
		inv.Detail += fmt.Sprintf("module holds %s but pools and queue account for %s; ", module, held)
	}
	if err := k.checkAccumulator(ctx, &inv); err != nil {
		return inv, err
	}
	return inv, nil
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
