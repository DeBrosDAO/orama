package keeper

import (
	"context"
	"fmt"
	"slices"

	"github.com/DeBrosOfficial/network/chain/x/houses/types"
)

// The methods below are how other modules read what a passed structural
// proposal enacted. x/houses never pushes these values; each consumer asks.

// EnactedEmissionSplit returns the emission split a passed structural
// proposal set, or nil while none has passed (the consumer then uses the
// canonical split).
func (k Keeper) EnactedEmissionSplit(ctx context.Context) (*types.EmissionSplitChange, error) {
	enacted, err := k.Enacted.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load enacted state: %w", err)
	}
	return enacted.EmissionSplit, nil
}

// CodeUploadAllowed reports whether the lowercase hex SHA-256 of a wasm code
// blob is on the enacted upload allow-list.
func (k Keeper) CodeUploadAllowed(ctx context.Context, sha256Hex string) (bool, error) {
	enacted, err := k.Enacted.Get(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to load enacted state: %w", err)
	}
	return slices.Contains(enacted.CodeUploadAllow, sha256Hex), nil
}

// AdapterAllowed reports whether adapter is on the enacted adapter
// allow-list. No module consumes this list yet: the shielded adapter path is
// not built (docs/CHAIN.md, "Governance enactment").
func (k Keeper) AdapterAllowed(ctx context.Context, adapter string) (bool, error) {
	enacted, err := k.Enacted.Get(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to load enacted state: %w", err)
	}
	return slices.Contains(enacted.AdapterAllow, adapter), nil
}
