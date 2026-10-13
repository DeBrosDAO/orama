package keeper

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/relay/types"
)

// EndBlock settles the oldest epoch that has reports waiting once its report
// window has passed. Settling is by epoch number, never by a message: no
// reporter, relay operator or caller chooses when an epoch is paid. At most one
// epoch settles per block; an epoch nobody reported on is never written, so an
// idle chain adds no state. It runs before x/emission's end block, which
// reconciles the mint it makes.
func (k Keeper) EndBlock(ctx sdk.Context) error {
	current, err := k.emission.CurrentEpoch(ctx)
	if err != nil {
		return fmt.Errorf("relay end block: failed to read the current epoch: %w", err)
	}
	epoch, found, err := k.oldestPendingEpoch(ctx)
	if err != nil {
		return err
	}
	if !found || current <= epoch || current-epoch <= types.ReportWindowEpochs {
		return nil
	}
	if _, err := k.SettleEpoch(ctx, epoch); err != nil {
		return fmt.Errorf("relay end block: %w", err)
	}
	return nil
}

// oldestPendingEpoch is the lowest epoch with a stored report or report chunk.
func (k Keeper) oldestPendingEpoch(ctx sdk.Context) (uint64, bool, error) {
	var oldest uint64
	var found bool

	reports, err := k.Reports.Iterate(ctx, nil)
	if err != nil {
		return 0, false, fmt.Errorf("relay end block: failed to iterate reports: %w", err)
	}
	defer reports.Close()
	if reports.Valid() {
		key, err := reports.Key()
		if err != nil {
			return 0, false, fmt.Errorf("relay end block: failed to read a report key: %w", err)
		}
		oldest, found = key.K1(), true
	}

	chunks, err := k.Chunks.Iterate(ctx, nil)
	if err != nil {
		return 0, false, fmt.Errorf("relay end block: failed to iterate chunks: %w", err)
	}
	defer chunks.Close()
	if chunks.Valid() {
		key, err := chunks.Key()
		if err != nil {
			return 0, false, fmt.Errorf("relay end block: failed to read a chunk key: %w", err)
		}
		if !found || key.K1() < oldest {
			oldest, found = key.K1(), true
		}
	}
	return oldest, found, nil
}
