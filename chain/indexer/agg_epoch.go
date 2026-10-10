package indexer

import (
	"context"
	"errors"
	"fmt"
	"time"

	"cosmossdk.io/math"
	abci "github.com/cometbft/cometbft/abci/types"
	cmttypes "github.com/cometbft/cometbft/types"

	emissiontypes "github.com/DeBrosOfficial/network/chain/x/emission/types"
	feestypes "github.com/DeBrosOfficial/network/chain/x/fees/types"
	powertypes "github.com/DeBrosOfficial/network/chain/x/power/types"
)

// blockFacts is what the aggregates take from one block.
type blockFacts struct {
	height int64
	time   time.Time
	commit *cmttypes.Commit
	// valsHash is the hash of the validator set that signs this block.
	valsHash []byte
	// events are the finalize-block events: what BeginBlock and EndBlock emitted.
	events []abci.Event
}

// aggregate folds one block into the epoch, supply and validator aggregates. It runs inside the
// block's own write batch, so a block is folded exactly once; the watch's Height also makes a
// repeat of the same block a no-op.
//
// The epoch in progress is tracked without asking the chain each block: x/emission closes an epoch
// in BeginBlock when both the duration and the block count have passed (emission.ShouldCloseEpoch),
// so the follower applies the same rule to the block's time and height and queries the chain only
// for a block where the rule holds. The query confirms the close and fails loudly if the chain
// disagrees, so a changed rule cannot silently skew the epochs.
func (f *Follower) aggregate(ctx context.Context, w *writer, b blockFacts) error {
	r := chainReader{ctx: ctx, chain: f.chain, height: b.height}
	wt, ok, err := w.watch()
	if err != nil {
		return err
	}
	if !ok {
		if wt, err = f.baseline(r, w, b); err != nil {
			return err
		}
	} else if b.height <= wt.Height {
		return nil
	}
	if err := f.foldSigning(ctx, w, wt, b); err != nil {
		return err
	}
	if err := f.foldEvents(w, wt, b); err != nil {
		return err
	}
	if wt, err = f.advance(r, w, wt, b); err != nil {
		return err
	}
	wt.Height, wt.ValSetHash = b.height, b.valsHash
	return w.putWatch(wt)
}

// baseline starts the aggregates at the index's first block from the chain's state after it: the
// epoch in progress and where it began, the closing rule, the running totals the first epoch's
// deltas are taken from, and the validators. The epoch's start height is exact: x/emission counts
// the blocks since the epoch began, so the epoch began that many blocks back. The epoch is partial
// unless that block closed the previous one: then the state it is read from is the epoch's start.
func (f *Follower) baseline(r chainReader, w *writer, b blockFacts) (watch, error) {
	state, err := r.emissionState()
	if err != nil {
		return watch{}, err
	}
	p, err := r.emissionParams()
	if err != nil {
		return watch{}, err
	}
	prev, err := r.cumulatives(state)
	if err != nil {
		return watch{}, err
	}
	refs, err := f.snapshotValidators(r, w, state.CurrentEpoch)
	if err != nil {
		return watch{}, err
	}
	for _, ref := range refs {
		if !ref.info.Jailed {
			continue
		}
		// Jailed before the index's first block: when is not known.
		if err := w.openJail(ref.cons, b.height, JailPeriod{}); err != nil {
			return watch{}, err
		}
	}
	return watch{
		Epoch: state.CurrentEpoch, StartHeight: b.height - int64(state.BlocksInEpoch) + 1,
		StartUnixNano: state.EpochStartUnixNano, DurationSec: p.EpochDurationSeconds,
		MinBlocks: p.MinBlocksPerEpoch, Partial: state.BlocksInEpoch > 0, Prev: prev,
	}, nil
}

// due reports whether the chain's closing rule holds at block b.
func (wt watch) due(b blockFacts) bool {
	elapsed := b.time.Sub(time.Unix(0, wt.StartUnixNano))
	blocks := b.height - wt.StartHeight + 1
	return elapsed >= time.Duration(wt.DurationSec)*time.Second && blocks >= 0 && uint64(blocks) >= wt.MinBlocks
}

// advance closes the epoch if block b closed it.
func (f *Follower) advance(r chainReader, w *writer, wt watch, b blockFacts) (watch, error) {
	if !wt.due(b) {
		return wt, nil
	}
	state, err := r.emissionState()
	if err != nil {
		return watch{}, err
	}
	switch {
	case state.CurrentEpoch == wt.Epoch:
		return watch{}, fmt.Errorf("epoch %d should have closed at block %d (%d seconds and %d blocks have passed since height %d) but the chain did not close it; this indexer's copy of the closing rule is out of date",
			wt.Epoch, b.height, wt.DurationSec, wt.MinBlocks, wt.StartHeight)
	case state.CurrentEpoch != wt.Epoch+1 || state.EpochStartUnixNano != b.time.UnixNano():
		return watch{}, fmt.Errorf("the chain is at epoch %d (started at %d) after block %d (time %d) while the index followed epoch %d: an epoch boundary was missed",
			state.CurrentEpoch, state.EpochStartUnixNano, b.height, b.time.UnixNano(), wt.Epoch)
	}
	return f.closeEpoch(r, w, wt, b, state)
}

// closeEpoch stores the closed epoch's row, supply point and validator rows, and returns the watch
// of the epoch that follows. Every figure is the chain's: the cumulative counters and balances at
// the closing height, the closing block's events, and the tallies the follower kept block by block.
func (f *Follower) closeEpoch(r chainReader, w *writer, wt watch, b blockFacts, state emissiontypes.EpochState) (watch, error) {
	cur, err := r.cumulatives(state)
	if err != nil {
		return watch{}, err
	}
	schedule, err := r.maxMintable(wt.Epoch)
	if err != nil {
		return watch{}, err
	}
	credited, bonded, err := rewardFlows(b.events)
	if err != nil {
		return watch{}, err
	}
	if err := w.setJSON(epochKey(wt.Epoch), newEpochRow(wt, b, cur, schedule, credited, bonded)); err != nil {
		return watch{}, err
	}
	point, err := supplyPoint(r, wt.Epoch, b, state, cur)
	if err != nil {
		return watch{}, err
	}
	if err := w.setJSON(supplyKey(wt.Epoch), point); err != nil {
		return watch{}, err
	}
	if err := f.closeValidators(r, w, wt.Epoch); err != nil {
		return watch{}, err
	}
	p, err := r.emissionParams()
	if err != nil {
		return watch{}, err
	}
	return watch{
		Epoch: state.CurrentEpoch, StartHeight: b.height + 1, StartUnixNano: state.EpochStartUnixNano,
		DurationSec: p.EpochDurationSeconds, MinBlocks: p.MinBlocksPerEpoch, Prev: cur,
	}, nil
}

func newEpochRow(wt watch, b blockFacts, cur cumulatives, schedule, credited, bonded math.Int) EpochRow {
	d := cur.since(wt.Prev)
	return EpochRow{
		Epoch: wt.Epoch, StartHeight: wt.StartHeight, StartTime: time.Unix(0, wt.StartUnixNano).UTC(),
		EndHeight: b.height, EndTime: b.time.UTC(), Blocks: b.height - wt.StartHeight + 1, Partial: wt.Partial,
		MaxMintable:      schedule.String(),
		MintedValidators: d.Minted.String(), MintedService: d.Service.String(),
		MintedDevelopment: d.Development.String(), MintedFaucet: d.Faucet.String(),
		MintedTotal: d.Minted.Add(d.Service).Add(d.Development).Add(d.Faucet).String(),
		Burned:      d.Burned.String(), FeesCollected: d.FeesCollected.String(),
		FeesBurned: d.FeesBurned.String(), FeesDistributed: d.FeesDistributed.String(),
		RewardsCredited: credited.String(), RewardsForceBonded: bonded.String(),
	}
}

// since is the change from an earlier set of totals to c.
func (c cumulatives) since(old cumulatives) cumulatives {
	return cumulatives{
		Minted: c.Minted.Sub(old.Minted), Burned: c.Burned.Sub(old.Burned),
		Development: c.Development.Sub(old.Development), Service: c.Service.Sub(old.Service),
		Faucet:        c.Faucet.Sub(old.Faucet),
		FeesCollected: c.FeesCollected.Sub(old.FeesCollected), FeesBurned: c.FeesBurned.Sub(old.FeesBurned),
		FeesDistributed: c.FeesDistributed.Sub(old.FeesDistributed),
	}
}

// rewardFlows reads how x/power paid the epoch's validator share out of the closing block's events.
// It moves the minted amount from x/emission to its own account, then sends each recipient's cut to
// x/fees (an earnings credit) and force-bonds part of a committee member's cut by sending it to the
// member's account. These are bank "transfer" events with a sender and recipient; the events carry
// no validator, so only the totals are known.
func rewardFlows(events []abci.Event) (credited, forceBonded math.Int, err error) {
	power, perr := moduleAddress(powertypes.ModuleName)
	fees, ferr := moduleAddress(feestypes.ModuleName)
	emission, eerr := moduleAddress(emissiontypes.ModuleName)
	if err := errors.Join(perr, ferr, eerr); err != nil {
		return math.Int{}, math.Int{}, fmt.Errorf("failed to address the reward modules: %w", err)
	}
	credited, forceBonded = math.ZeroInt(), math.ZeroInt()
	for _, e := range events {
		if e.Type != "transfer" {
			continue
		}
		sender, recipient, amount := attr(e, "sender"), attr(e, "recipient"), attr(e, "amount")
		if sender != power {
			continue
		}
		n, err := sumCoins(amount)
		if err != nil {
			return math.Int{}, math.Int{}, fmt.Errorf("transfer event of x/power: %w", err)
		}
		switch recipient {
		case fees:
			credited = credited.Add(n)
		case emission:
			// An unpayable validator's share returned to x/emission; it was never credited.
		default:
			forceBonded = forceBonded.Add(n)
		}
	}
	return credited, forceBonded, nil
}

// attr returns the value of the first attribute of e named key, or "".
func attr(e abci.Event, key string) string {
	for _, a := range e.Attributes {
		if a.Key == key {
			return a.Value
		}
	}
	return ""
}
