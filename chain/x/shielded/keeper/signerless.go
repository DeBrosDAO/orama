package keeper

import (
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"

	"github.com/DeBrosOfficial/network/chain/x/shielded/types"
)

// ErrSignerlessBlockFull means the block already holds max_signerless_per_block signer-less
// transfers.
var ErrSignerlessBlockFull = errors.New("the block holds its limit of signer-less shielded transfers")

// takeSignerlessSlot counts a signer-less transfer against the block's limit. The count lives in the
// transient store, so a failed tx gives its slot back and the next block starts at zero.
func (k Keeper) takeSignerlessSlot(ctx context.Context, p types.Params) error {
	n, err := k.signerless.Get(ctx)
	if err != nil && !errors.Is(err, collections.ErrNotFound) {
		return fmt.Errorf("read the signer-less count: %w", err)
	}
	if n >= uint64(p.MaxSignerlessPerBlock) {
		return fmt.Errorf("%w (%d)", ErrSignerlessBlockFull, p.MaxSignerlessPerBlock)
	}
	return k.signerless.Set(ctx, n+1)
}
