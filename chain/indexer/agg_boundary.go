package indexer

import (
	"fmt"
	"strconv"

	abci "github.com/cometbft/cometbft/abci/types"

	emissiontypes "github.com/DeBrosOfficial/network/chain/x/emission/types"
)

// Where an emission epoch closes depends on params the chain can change (the epoch duration and the
// minimum block count), and on the state of the epoch, which a node stops keeping after its pruning
// window. A rule worked out from the params the epoch began under goes wrong as soon as the params
// change during an epoch the index could not read: it closes the epoch at the wrong block, and the
// chain, once its state is readable, disagrees about the boundary.
//
// So the chain says it. x/emission emits emission_epoch_closed in the block that closes an epoch
// (epoch, blocks, start_unix_nano), and the aggregator closes the epoch at the block that carries it,
// whatever the rule says. The decision then needs no state and no params.
//
// A binary that did not emit the event leaves the closing rule as the only way to tell, and a block
// does not say which binary produced it. The same rule as for settles and compresses applies
// (chain_events.go): the aggregator records the first height at which it saw the event
// (keyEpochEventsFrom).
//
//   - Before it, a close is decided by the closing rule the watch holds, as it always was. The
//     first close after the chain moved to a binary that emits the event carries it, so every close
//     before it is older. A rule that was wrong there (params changed in a window whose state was
//     pruned) cannot be recovered from anything the chain still keeps, and the close that follows
//     it stops the index when the chain's state disagrees.
//   - From it on, only the event closes an epoch. A block the rule would close but that carries no
//     event does not, and an event for another epoch than the one followed, or whose block count
//     or start disagree with what the aggregator counted itself, stops the index.
//
// The decision depends only on the chain's blocks in order, so every indexer of the chain closes the
// same epochs at the same blocks whatever height it started at or what its node had pruned.

// epochClosedEvent is the emission_epoch_closed event of a block.
type epochClosedEvent struct {
	epoch, blocks uint64
	startUnixNano int64
}

// readEpochClosed returns the epoch close the block's events report, if any. A block closes at most
// one epoch.
func readEpochClosed(events []abci.Event) (epochClosedEvent, bool, error) {
	var out epochClosedEvent
	found := false
	for _, e := range events {
		if e.Type != emissiontypes.EventTypeEpochClosed {
			continue
		}
		if found {
			return out, false, fmt.Errorf("the block reports two %s events", e.Type)
		}
		var err error
		if out, err = parseEpochClosed(e); err != nil {
			return out, false, err
		}
		found = true
	}
	return out, found, nil
}

func parseEpochClosed(e abci.Event) (epochClosedEvent, error) {
	const typ = emissiontypes.EventTypeEpochClosed
	var out epochClosedEvent
	var err error
	if out.epoch, err = strconv.ParseUint(attr(e, emissiontypes.AttributeKeyEpoch), 10, 64); err != nil {
		return out, fmt.Errorf("%s event epoch %q: %w", typ, attr(e, emissiontypes.AttributeKeyEpoch), err)
	}
	if out.blocks, err = strconv.ParseUint(attr(e, emissiontypes.AttributeKeyBlocks), 10, 64); err != nil {
		return out, fmt.Errorf("%s event blocks %q: %w", typ, attr(e, emissiontypes.AttributeKeyBlocks), err)
	}
	if out.startUnixNano, err = strconv.ParseInt(attr(e, emissiontypes.AttributeKeyStartUnixNano), 10, 64); err != nil {
		return out, fmt.Errorf("%s event start_unix_nano %q: %w", typ, attr(e, emissiontypes.AttributeKeyStartUnixNano), err)
	}
	return out, nil
}

// noteEpochEvents records the first height at which the chain reported an epoch close, whether or
// not the aggregates are running at this block.
func (w *writer) noteEpochEvents(b blockFacts) error {
	_, found, err := readEpochClosed(b.events)
	if err != nil || !found {
		return err
	}
	if _, seen, err := w.eventsFrom(keyEpochEventsFrom); err != nil || seen {
		return err
	}
	return w.setEventsFrom(keyEpochEventsFrom, b.height)
}

// closesAt reports whether block b closed the epoch the watch follows, by the rule above.
func (wt watch) closesAt(w *writer, b blockFacts) (bool, error) {
	ev, found, err := readEpochClosed(b.events)
	if err != nil {
		return false, err
	}
	if found {
		return wt.checkClose(ev, b)
	}
	_, seen, err := w.eventsFrom(keyEpochEventsFrom)
	if err != nil || seen {
		return false, err
	}
	return wt.due(b), nil
}

// checkClose holds the close the chain reported against what the aggregator knows of the epoch. A
// baseline taken at the closing block already reads the epoch that follows (its start is after b), so
// that block's event belongs to the epoch before the watch's and closes nothing.
func (wt watch) checkClose(ev epochClosedEvent, b blockFacts) (bool, error) {
	if wt.StartHeight > b.height {
		if ev.epoch+1 != wt.Epoch {
			return false, fmt.Errorf("the chain closed epoch %d at block %d while the index follows epoch %d from block %d: an epoch boundary was missed",
				ev.epoch, b.height, wt.Epoch, wt.StartHeight)
		}
		return false, nil
	}
	if ev.epoch != wt.Epoch {
		return false, fmt.Errorf("the chain closed epoch %d at block %d while the index followed epoch %d: an epoch boundary was missed", ev.epoch, b.height, wt.Epoch)
	}
	if blocks := uint64(b.height - wt.StartHeight + 1); ev.blocks != blocks || ev.startUnixNano != wt.StartUnixNano {
		return false, fmt.Errorf("the chain closed epoch %d at block %d after %d blocks from %d, the index counted %d blocks from %d: an epoch boundary was missed",
			ev.epoch, b.height, ev.blocks, ev.startUnixNano, blocks, wt.StartUnixNano)
	}
	return true, nil
}
