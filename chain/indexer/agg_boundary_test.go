package indexer

import (
	"context"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	abci "github.com/cometbft/cometbft/abci/types"

	emissiontypes "github.com/DeBrosOfficial/network/chain/x/emission/types"
)

func epochClosedEventOf(epoch, blocks uint64, startUnixNano int64) abci.Event {
	return abci.Event{Type: emissiontypes.EventTypeEpochClosed, Attributes: attrs(
		emissiontypes.AttributeKeyEpoch, strconv.FormatUint(epoch, 10),
		emissiontypes.AttributeKeyBlocks, strconv.FormatUint(blocks, 10),
		emissiontypes.AttributeKeyStartUnixNano, strconv.FormatInt(startUnixNano, 10))}
}

// addLedgerBlocks adds blocks from..to to a chain whose ledger decides where epochs close, as a
// binary that reports closes would produce them: the block that closes an epoch carries x/power's
// reward transfers and the emission_epoch_closed event. Every validator votes in every block.
func addLedgerBlocks(t *testing.T, chain *fakeChain, from, to int64) {
	t.Helper()
	for h := from; h <= to; h++ {
		var events []abci.Event
		if epoch, blocks, start, closed := chain.ledger.closeAt(h); closed {
			events = append(rewardEvents(t), epochClosedEventOf(epoch, blocks, start))
		}
		chain.addBlock(votes(chain.valset, true, true, true), events)
	}
}

// retuned is a chain whose epochs are three blocks long until the chain changes its params at height
// 4 to five blocks and five seconds: epoch 1 closes at 3, epoch 2 at 8, epoch 3 at 13. A follower
// that predicted epoch 2 from the params epoch 1 ended under would close it at 6.
func retuned(t *testing.T) *fakeChain {
	t.Helper()
	chain, _ := econChain(t)
	chain.ledger.changes = map[int64]epochRule{4: {durationSec: 5, minBlocks: 5}}
	return chain
}

func TestEpochs_aParamsChangeMovesTheBoundaryTheFollowerTakesFromTheChain(t *testing.T) {
	chain := retuned(t)
	addLedgerBlocks(t, chain, 1, 14)
	store, _ := follow(t, chain, t.TempDir(), 1)
	defer store.Close()

	e1, e2, e3 := epochOf(t, store, 1), epochOf(t, store, 2), epochOf(t, store, 3)
	require.Equal(t, int64(3), e1.EndHeight)
	require.Equal(t, int64(4), e2.StartHeight)
	require.Equal(t, int64(8), e2.EndHeight, "the chain's rule changed, and the boundary is where the chain says it is")
	require.Equal(t, int64(9), e3.StartHeight)
	require.Equal(t, int64(13), e3.EndHeight)
	require.False(t, e2.Unavailable)
	require.Equal(t, "600", e2.MintedValidators)
	st, err := store.Status()
	require.NoError(t, err)
	require.Equal(t, int64(14), st.Cursor)
}

// The node pruned the states of the blocks in which the params changed and two epochs closed. The
// rule the follower carried across the unreadable closes is stale, and it must not decide where
// they end: it would close epoch 2 at 6, and the readable close at 13 would then disagree with the
// chain about the boundary for as long as the follower ran.
func TestEpochs_aParamsChangeInAnUnreadableWindowDoesNotMoveTheBoundaries(t *testing.T) {
	chain := retuned(t)
	addLedgerBlocks(t, chain, 1, 2)
	store, f := follow(t, chain, t.TempDir(), 1)
	defer store.Close()
	addLedgerBlocks(t, chain, 3, 16)
	chain.prunedBelow = 11

	_, err := f.Step(context.Background())
	require.NoError(t, err)

	e1, e2, e3 := epochOf(t, store, 1), epochOf(t, store, 2), epochOf(t, store, 3)
	require.True(t, e1.Unavailable)
	require.Equal(t, int64(3), e1.EndHeight)
	require.True(t, e2.Unavailable)
	require.Equal(t, int64(4), e2.StartHeight)
	require.Equal(t, int64(8), e2.EndHeight, "not 6, where the rule epoch 1 ended under would have put it")
	require.Equal(t, int64(9), e3.StartHeight)
	require.Equal(t, int64(13), e3.EndHeight, "closed at a readable height: the chain confirmed the boundary")
	require.Contains(t, e3.UnavailableReason, "end of the epoch before")
	w := store.newWriter()
	defer w.close()
	wt, ok, err := w.watch()
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint64(4), wt.Epoch)
	require.Equal(t, int64(14), wt.StartHeight, "and the epoch after it begins where the chain closed epoch 3")
}

func TestEpochs_aBaselineOnAClosingBlockIgnoresTheEventItAlreadyReads(t *testing.T) {
	chain, _ := econChain(t)
	addLedgerBlocks(t, chain, 1, 7)
	store, _ := follow(t, chain, t.TempDir(), 3)
	defer store.Close()

	e2 := epochOf(t, store, 2)
	require.Equal(t, int64(4), e2.StartHeight)
	require.Equal(t, int64(6), e2.EndHeight)
	require.False(t, e2.Partial, "block 3 closed epoch 1: the state read after it is epoch 2's start")
}

func TestEpochs_aFirstEventIsRecordedWhereTheAggregatesAreNotRunning(t *testing.T) {
	chain, _ := econChain(t)
	addLedgerBlocks(t, chain, 1, 4)
	chain.prunedBelow = 100
	store, _ := follow(t, chain, t.TempDir(), 1)
	defer store.Close()

	from, seen, err := getUint(store.db, []byte(keyEpochEventsFrom))
	require.NoError(t, err)
	require.True(t, seen, "the index has seen the chain report a close, though it could not read the state")
	require.Equal(t, uint64(3), from)
}

func TestEpochs_aBinaryWithoutTheEventStillClosesByTheRule(t *testing.T) {
	chain, _ := econChain(t)
	addEconBlocks(t, chain, 1, 9)
	store, _ := follow(t, chain, t.TempDir(), 1)
	defer store.Close()

	require.Equal(t, int64(6), epochOf(t, store, 2).EndHeight)
	_, seen, err := getUint(store.db, []byte(keyEpochEventsFrom))
	require.NoError(t, err)
	require.False(t, seen)
}

func TestEpochs_aCloseTheIndexDoesNotAgreeWithStopsTheFollower(t *testing.T) {
	for name, ev := range map[string]abci.Event{
		"another epoch":       epochClosedEventOf(2, 3, blockTime(0).UnixNano()),
		"another block count": epochClosedEventOf(1, 2, blockTime(0).UnixNano()),
		"another start":       epochClosedEventOf(1, 3, blockTime(1).UnixNano()),
		"a malformed epoch":   {Type: emissiontypes.EventTypeEpochClosed, Attributes: attrs(emissiontypes.AttributeKeyEpoch, "x")},
	} {
		t.Run(name, func(t *testing.T) {
			chain, _ := econChain(t)
			addLedgerBlocks(t, chain, 1, 2)
			chain.addBlock(votes(chain.valset, true, true, true), append(rewardEvents(t), ev))
			store := openStore(t, t.TempDir())
			defer store.Close()
			f, err := NewFollower(chain, store, 1)
			require.NoError(t, err)
			_, err = f.Step(context.Background())
			require.Error(t, err)
			st, err := store.Status()
			require.NoError(t, err)
			require.Equal(t, int64(2), st.Cursor, "the index stops before the block it cannot explain")
		})
	}
}

func TestEpochs_twoCloseEventsInOneBlockAreRefused(t *testing.T) {
	_, _, err := readEpochClosed([]abci.Event{epochClosedEventOf(1, 3, 0), epochClosedEventOf(2, 3, 0)})
	require.ErrorContains(t, err, "two emission_epoch_closed events")
}
