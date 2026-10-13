package ops

import (
	"context"
	"fmt"
	"sync"

	"cosmossdk.io/math"

	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
)

// Balance returns addr's balance of denom.
func (c *Chain) Balance(ctx context.Context, addr, denom string) (math.Int, error) {
	var resp banktypes.QueryBalanceResponse
	if err := c.Client.Query(ctx, "/cosmos.bank.v1beta1.Query/Balance", &banktypes.QueryBalanceRequest{Address: addr, Denom: denom}, &resp); err != nil {
		return math.Int{}, fmt.Errorf("read the %s balance of %s: %w", denom, addr, err)
	}
	if resp.Balance == nil {
		return math.ZeroInt(), nil
	}
	return resp.Balance.Amount, nil
}

// Each runs fn for every index in [0, n) with at most parallel calls in flight and returns the
// first error. It waits for the calls it started before returning.
func Each(ctx context.Context, n, parallel int, fn func(ctx context.Context, i int) error) error {
	if parallel < 1 {
		parallel = 1
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
		slots    = make(chan struct{}, parallel)
	)
	for i := 0; i < n; i++ {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			defer func() { <-slots }()
			if err := fn(ctx, i); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("item %d: %w", i, err)
					cancel()
				}
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if firstErr != nil {
		return firstErr
	}
	return ctx.Err()
}
