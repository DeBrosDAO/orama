package indexer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

// The nodes the installer sets up keep the last 100 states and prune every 10 blocks
// (core/pkg/install/chainconfig.go), while they keep blocks for much longer. An indexer reads the
// chain's state at the heights it indexes, so one that starts late, or falls behind by more than
// those states, asks for state that is gone. That is not a failure to retry: the state never comes
// back. The aggregates therefore start at the first height whose state the node has, and an epoch
// that closed while its state was gone is recorded as unavailable. Nothing is read from a later
// state in its place, and no change spanning several epochs is put on one.

// Why an epoch is unavailable. The reasons are shown by the API.
const (
	reasonStatePruned = "the node no longer kept the chain state at the epoch's closing height (height %d), because the indexer was behind or stopped while the epoch closed; the figures read from the chain cannot be known"
	reasonPrevUnread  = "the chain state at the end of the epoch before could not be read, so the change over this epoch alone cannot be told from the change over several"
)

// prunedState is what an error from Chain.QueryAt answers when the node holds no state for the
// height asked (*node.QueryError).
type prunedState interface{ StatePruned() bool }

// isStatePruned reports whether err says the node holds no state for the height queried. Any other
// error, including a missing record or an unreachable node, is not.
func isStatePruned(err error) bool {
	var p prunedState
	return errors.As(err, &p) && p.StatePruned()
}

// canFold reports whether the block at height is folded into the aggregates: they are running, or
// the node still keeps the state at height that their baseline reads. An index that began, or fell
// behind, below the node's states indexes the blocks and transactions all the same and starts the
// aggregates at the first height whose state is there. Any other error stops the block.
func (f *Follower) canFold(ctx context.Context, w *writer, height int64) (bool, error) {
	if _, running, err := w.watch(); err != nil || running {
		return running, err
	}
	r := chainReader{ctx: ctx, chain: f.chain, height: height}
	if _, err := r.emissionState(); err != nil {
		if isStatePruned(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// closeUnreadable closes epoch wt.Epoch at block b without its figures: the node no longer keeps
// the state they are read from. The epoch row says so, the validators' tallies of the epoch are
// dropped (their status and power are state too), and the next epoch begins after b with its start
// totals unknown until an epoch closes at a height whose state is there.
//
// The boundary b is the chain's own when it reported the close in an event (agg_boundary.go), and
// so is the next epoch's, whatever became of the closing rule meanwhile: the rule the watch carries
// on is not consulted once the chain has reported a close. It matters only for blocks of a binary
// that emitted no such event, where it is the rule the epoch began under; the next close whose
// state is readable checks it against the chain, as for every epoch, and stops the index if it
// was wrong.
func (f *Follower) closeUnreadable(w *writer, wt watch, b blockFacts, rw epochRewards) (watch, error) {
	row := unavailableRow(wt, b, fmt.Sprintf(reasonStatePruned, b.height))
	if err := rw.apply(&row); err != nil {
		return watch{}, err
	}
	if err := w.setJSON(epochKey(wt.Epoch), row); err != nil {
		return watch{}, err
	}
	if err := w.dropEpochCounters(wt.Epoch); err != nil {
		return watch{}, err
	}
	slog.Warn("an epoch closed while the node no longer kept its state; the epoch is recorded as unavailable",
		"epoch", wt.Epoch, "height", b.height)
	return watch{
		Epoch: wt.Epoch + 1, StartHeight: b.height + 1, StartUnixNano: b.time.UnixNano(),
		DurationSec: wt.DurationSec, MinBlocks: wt.MinBlocks, PrevUnknown: true,
	}, nil
}

// unavailableRow is the row of an epoch whose figures are not known: where it began and ended, and
// why. Every figure read from state is left out.
func unavailableRow(wt watch, b blockFacts, reason string) EpochRow {
	return EpochRow{
		Epoch: wt.Epoch, StartHeight: wt.StartHeight, StartTime: epochStart(wt),
		EndHeight: b.height, EndTime: b.time.UTC(), Blocks: b.height - wt.StartHeight + 1, Partial: wt.Partial,
		Unavailable: true, UnavailableReason: reason,
	}
}
