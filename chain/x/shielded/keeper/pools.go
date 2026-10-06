package keeper

import (
	"context"
	"errors"
	"fmt"
	"time"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"

	"github.com/DeBrosOfficial/network/chain/x/shielded/pool"
	"github.com/DeBrosOfficial/network/chain/x/shielded/types"
)

// nativePool is the only pool that exists: vintage 1, the native asset. Multi-asset stays off
// until the structural vote, so no message can name another asset (pool.AllowAsset).
func nativePool() collections.Pair[uint32, []byte] {
	return collections.Join(types.VintageOrchardV1, pool.NativeAsset[:])
}

func (k Keeper) poolBalance(ctx context.Context, key collections.Pair[uint32, []byte]) (math.Int, error) {
	bal, err := k.Pools.Get(ctx, key)
	if errors.Is(err, collections.ErrNotFound) {
		return math.ZeroInt(), nil
	}
	if err != nil {
		return math.Int{}, fmt.Errorf("read pool balance: %w", err)
	}
	return bal, nil
}

// credit adds to a pool.
func (k Keeper) credit(ctx context.Context, key collections.Pair[uint32, []byte], amount math.Int) error {
	if !amount.IsPositive() {
		return pool.ErrAmount
	}
	bal, err := k.poolBalance(ctx, key)
	if err != nil {
		return err
	}
	return k.Pools.Set(ctx, key, bal.Add(amount))
}

// debitPool takes from a pool. The turnstile: a pool never pays out more than went in.
func (k Keeper) debitPool(ctx context.Context, key collections.Pair[uint32, []byte], amount math.Int) error {
	if !amount.IsPositive() {
		return pool.ErrAmount
	}
	bal, err := k.poolBalance(ctx, key)
	if err != nil {
		return err
	}
	if bal.LT(amount) {
		return fmt.Errorf("%w: pool %d holds %s, %s was asked", pool.ErrUnderflow, key.K1(), bal, amount)
	}
	return k.Pools.Set(ctx, key, bal.Sub(amount))
}

func (k Keeper) limiter(ctx context.Context, key collections.Pair[uint32, []byte]) (pool.Limiter, types.Limiter, error) {
	stored, err := k.Limiters.Get(ctx, key)
	if errors.Is(err, collections.ErrNotFound) {
		return pool.Limiter{Counted: math.ZeroInt()}, types.Limiter{Vintage: key.K1(), Asset: key.K2()}, nil
	}
	if err != nil {
		return pool.Limiter{}, types.Limiter{}, fmt.Errorf("read unshield limiter: %w", err)
	}
	lim := pool.Limiter{Counted: stored.Counted}
	if stored.WindowStart != 0 {
		lim.Start = time.Unix(stored.WindowStart, 0).UTC()
	}
	return lim, stored, nil
}

func (k Keeper) saveLimiter(ctx context.Context, key collections.Pair[uint32, []byte], lim pool.Limiter, stored types.Limiter) error {
	stored.WindowStart = 0
	if !lim.Start.IsZero() {
		stored.WindowStart = lim.Start.Unix()
	}
	stored.Counted = lim.Counted
	return k.Limiters.Set(ctx, key, stored)
}

// applyCap runs one outflow through the pool's 24h limiter and returns the verdict. balance is the
// pool's balance before the outflow.
func (k Keeper) applyCap(
	ctx context.Context, now time.Time, key collections.Pair[uint32, []byte],
	balance, amount math.Int, kind pool.Kind,
) (pool.Outcome, error) {
	p, err := k.params(ctx)
	if err != nil {
		return pool.OutcomeReject, err
	}
	lim, stored, err := k.limiter(ctx, key)
	if err != nil {
		return pool.OutcomeReject, err
	}
	outcome, applyErr := lim.Apply(now, balance, p.UnshieldFloor, amount, kind)
	if applyErr != nil {
		return outcome, applyErr
	}
	if err := k.saveLimiter(ctx, key, lim, stored); err != nil {
		return pool.OutcomeReject, fmt.Errorf("write unshield limiter: %w", err)
	}
	return outcome, nil
}
