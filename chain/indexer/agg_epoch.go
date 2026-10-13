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
// The epoch in progress is tracked without asking the chain each block. x/emission reports the close
// in an emission_epoch_closed event of the closing block, and the follower closes the epoch at the
// block that carries it (agg_boundary.go). Only for blocks of a binary that did not emit the event
// does it apply the closing rule x/emission used (emission.ShouldCloseEpoch: both the duration and
// the block count have passed) to the block's time and height. Either way the chain's state, when
// it is readable, confirms the close and fails loudly if the chain disagrees.
func (f *Follower) aggregate(ctx context.Context, w *writer, b blockFacts) error {
	if err := w.noteEpochEvents(b); err != nil {
		return err
	}
	if !f.folding {
		return nil
	}
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
// The block is the index's first, or, when the node had pruned the state of the blocks before it,
// the first whose state it still keeps (agg_pruned.go): the aggregates begin there, and the status
// says so.
func (f *Follower) baseline(r chainReader, w *writer, b blockFacts) (watch, error) {
	if err := w.setAggregatesFrom(b.height); err != nil {
		return watch{}, err
	}
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

// due reports whether the closing rule the watch holds is met at block b. It decides a close only
// for blocks before the chain reported its closes in events (closesAt).
func (wt watch) due(b blockFacts) bool {
	elapsed := b.time.Sub(time.Unix(0, wt.StartUnixNano))
	blocks := b.height - wt.StartHeight + 1
	return elapsed >= time.Duration(wt.DurationSec)*time.Second && blocks >= 0 && uint64(blocks) >= wt.MinBlocks
}

// advance closes the epoch if block b closed it. When the node no longer keeps the state of b, the
// epoch is closed as unavailable (closeUnreadable) and the index goes on.
func (f *Follower) advance(r chainReader, w *writer, wt watch, b blockFacts) (watch, error) {
	closes, err := wt.closesAt(w, b)
	if err != nil || !closes {
		return wt, err
	}
	rw, err := readRewards(wt.Epoch, b.events)
	if err != nil {
		return watch{}, err
	}
	next, err := f.closeFromState(r, w, wt, b, rw)
	if isStatePruned(err) {
		next, err = f.closeUnreadable(w, wt, b, rw)
	}
	if err != nil {
		return watch{}, err
	}
	return next, w.putRewards(wt.Epoch, rw)
}

// closeFromState confirms against the chain that block b closed the epoch and reads the epoch's
// figures from its state.
func (f *Follower) closeFromState(r chainReader, w *writer, wt watch, b blockFacts, rw epochRewards) (watch, error) {
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
	return f.closeEpoch(r, w, wt, b, state, rw)
}

// closeEpoch stores the closed epoch's row, supply point and validator rows, and returns the watch
// of the epoch that follows. Every figure is the chain's: the cumulative counters and balances at
// the closing height, the closing block's events, and the tallies the follower kept block by block.
// It reads all it needs from the chain before it writes anything but the validator registry, so a
// state the node has pruned part way through leaves nothing of the epoch behind.
//
// An epoch that follows one closed as unavailable (wt.PrevUnknown) has the supply and validators
// of its closing height, which are exact, but its change in the chain's totals is the change since
// the epoch before it too, so its row is unavailable as well. The totals read here start the next.
func (f *Follower) closeEpoch(r chainReader, w *writer, wt watch, b blockFacts, state emissiontypes.EpochState, rw epochRewards) (watch, error) {
	cur, err := r.cumulatives(state)
	if err != nil {
		return watch{}, err
	}
	schedule, err := r.maxMintable(wt.Epoch)
	if err != nil {
		return watch{}, err
	}
	point, err := supplyPoint(r, wt.Epoch, b, state, cur)
	if err != nil {
		return watch{}, err
	}
	p, err := r.emissionParams()
	if err != nil {
		return watch{}, err
	}
	if err := f.closeValidators(r, w, wt.Epoch); err != nil {
		return watch{}, err
	}
	row := newEpochRow(wt, b, cur, schedule, rw)
	if wt.PrevUnknown {
		row = unavailableRow(wt, b, reasonPrevUnread)
	}
	if err := rw.apply(&row); err != nil {
		return watch{}, err
	}
	if err := w.setJSON(epochKey(wt.Epoch), row); err != nil {
		return watch{}, err
	}
	if err := w.setJSON(supplyKey(wt.Epoch), point); err != nil {
		return watch{}, err
	}
	return watch{
		Epoch: state.CurrentEpoch, StartHeight: b.height + 1, StartUnixNano: state.EpochStartUnixNano,
		DurationSec: p.EpochDurationSeconds, MinBlocks: p.MinBlocksPerEpoch, Prev: cur,
	}, nil
}

// epochStart is the time the epoch in progress began, as the chain recorded it.
func epochStart(wt watch) time.Time { return time.Unix(0, wt.StartUnixNano).UTC() }

func newEpochRow(wt watch, b blockFacts, cur cumulatives, schedule math.Int, rw epochRewards) EpochRow {
	d := cur.since(wt.Prev)
	return EpochRow{
		Epoch: wt.Epoch, StartHeight: wt.StartHeight, StartTime: epochStart(wt),
		EndHeight: b.height, EndTime: b.time.UTC(), Blocks: b.height - wt.StartHeight + 1, Partial: wt.Partial,
		MaxMintable:      schedule.String(),
		MintedValidators: d.Minted.String(), MintedService: d.Service.String(),
		MintedDevelopment: d.Development.String(), MintedFaucet: d.Faucet.String(),
		MintedTotal: d.Minted.Add(d.Service).Add(d.Development).Add(d.Faucet).String(),
		Burned:      d.Burned.String(), FeesCollected: d.FeesCollected.String(),
		FeesBurned: d.FeesBurned.String(), FeesDistributed: d.FeesDistributed.String(),
		RewardsCredited: rw.credited.String(), RewardsForceBonded: rw.bonded.String(),
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
