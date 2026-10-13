package keeper

import sdk "github.com/cosmos/cosmos-sdk/types"

// Isolate exposes isolate to the black-box tests.
func (k Keeper) Isolate(ctx sdk.Context, kind, subject string, fn func(sdk.Context) error) (bool, error) {
	return k.isolate(ctx, kind, subject, fn)
}

// Reject exposes reject to the black-box tests.
func Reject(err error) error { return reject(err) }
