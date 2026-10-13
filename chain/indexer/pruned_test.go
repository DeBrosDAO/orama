package indexer

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	abci "github.com/cometbft/cometbft/abci/types"
	cmttypes "github.com/cometbft/cometbft/types"

	slashingtypes "github.com/cosmos/cosmos-sdk/x/slashing/types"

	"github.com/DeBrosOfficial/network/chain/client/node"
)

// putTx puts a transaction in the block at height of the fake chain.
func putTx(chain *fakeChain, height int64, tx fakeTx) {
	chain.blocks[height-1].txs = []cmttypes.Tx{tx.raw}
	chain.blocks[height-1].results = []*abci.ExecTxResult{tx.res}
}

// newPrunedFollower binds a follower to a store in dir and takes no step.
func newPrunedFollower(t *testing.T, chain *fakeChain, dir string, start int64) (*Store, *Follower) {
	t.Helper()
	store := openStore(t, dir)
	f, err := NewFollower(chain, store, start)
	require.NoError(t, err)
	return store, f
}

func TestIsStatePruned_onlyTheNodesPrunedStateAnswerIs(t *testing.T) {
	pruned := prunedStateError("/orama.emission.v1.Query/CurrentEpoch", 5)
	require.True(t, isStatePruned(pruned))
	require.True(t, isStatePruned(fmt.Errorf("query at height 5: %w", pruned)), "through the wrapping the reader adds")
	require.True(t, isStatePruned(errors.Join(errors.New("other"), pruned)))

	missing := &node.QueryError{Codespace: "sdk", Code: 22, Log: "rpc error: code = NotFound desc = key not found"}
	require.False(t, isStatePruned(missing), "a missing record is not a pruned state")
	require.False(t, isStatePruned(&node.QueryError{Codespace: "sdk", Code: 38, Log: "listing 7: not found"}))
	require.False(t, isStatePruned(errors.New("connection refused")))
	require.False(t, isStatePruned(errors.New("failed to load state at height 5; version mismatch on immutable IAVL tree; version does not exist. Version has either been pruned")),
		"words alone, from no node answer, are not enough")
	require.False(t, isStatePruned(nil))
}

func TestEpochs_aStartOnAPrunedChainBeginsTheAggregatesWhereTheStateIs(t *testing.T) {
	chain, vals := econChain(t)
	unjail := okTx(t, one(&slashingtypes.MsgUnjail{ValidatorAddr: vals[2].operator}), one(&slashingtypes.MsgUnjailResponse{}), addr(t, 5))
	addEconBlocks(t, chain, 1, 20)
	putTx(chain, 2, unjail)
	chain.prunedBelow = 15
	store, f := newPrunedFollower(t, chain, t.TempDir(), 1)
	defer store.Close()

	n, err := f.Step(context.Background())
	require.NoError(t, err, "a chain whose early state is pruned is not a failure")
	require.Equal(t, 20, n, "every block is indexed")
	st, err := store.Status()
	require.NoError(t, err)
	require.Equal(t, int64(1), st.StartHeight)
	require.Equal(t, int64(20), st.Cursor)
	require.Equal(t, int64(15), st.AggregatesFrom, "the first height whose state the node kept")
	require.Nil(t, st.Stuck)
	for _, key := range chain.asked {
		method, height, _ := strings.Cut(key, "@")
		if h, err := strconv.Atoi(height); err == nil && h < 15 && h > 0 {
			require.Equal(t, queryEmissionState, method, "before the baseline only the probe for the state is asked, even for the unjail of block 2: %s", key)
		}
	}

	_, ok, err := store.Epoch(5)
	require.NoError(t, err)
	require.False(t, ok, "epochs that closed before the state kept are not in the index")
	e6 := epochOf(t, store, 6)
	require.Equal(t, int64(16), e6.StartHeight, "block 15 closed epoch 5: the state read after it is epoch 6's start")
	require.False(t, e6.Partial)
	require.False(t, e6.Unavailable)
	require.Equal(t, "6", e6.Burned, "its three blocks, exact")
	require.Equal(t, "600", e6.MintedValidators)
	_, found, err := store.EpochValidators(6, 1, 10)
	require.NoError(t, err)
	require.True(t, found)
}

func TestEpochs_aBaselineInsideAnEpochIsPartial(t *testing.T) {
	chain, _ := econChain(t)
	addEconBlocks(t, chain, 1, 19)
	chain.prunedBelow = 14
	store, f := newPrunedFollower(t, chain, t.TempDir(), 1)
	defer store.Close()

	_, err := f.Step(context.Background())
	require.NoError(t, err)
	st, err := store.Status()
	require.NoError(t, err)
	require.Equal(t, int64(14), st.AggregatesFrom)
	e5 := epochOf(t, store, 5)
	require.True(t, e5.Partial, "block 14 is the second of epoch 5: the blocks before it were not folded")
	require.Equal(t, int64(13), e5.StartHeight, "the chain says how many blocks the epoch had")
	require.False(t, e5.Unavailable)
	require.Equal(t, "2", e5.Burned, "only the block after the baseline block")
	require.False(t, epochOf(t, store, 6).Partial)
}

func TestEpochs_aStartThatNeverFindsStateIndexesBlocksAndRetriesTheBaseline(t *testing.T) {
	chain, _ := econChain(t)
	addEconBlocks(t, chain, 1, 5)
	chain.prunedBelow = 100
	store, f := newPrunedFollower(t, chain, t.TempDir(), 1)
	defer store.Close()

	n, err := f.Step(context.Background())
	require.NoError(t, err)
	require.Equal(t, 5, n)
	st, err := store.Status()
	require.NoError(t, err)
	require.Zero(t, st.AggregatesFrom, "no state was ever kept: the aggregates have not begun")
	rows, err := store.Epochs(1, 10)
	require.NoError(t, err)
	require.Empty(t, rows)
	b, ok, err := store.Block(5)
	require.NoError(t, err)
	require.True(t, ok)
	require.NotEmpty(t, b.Hash)

	chain.prunedBelow = 0
	addEconBlocks(t, chain, 6, 7)
	_, err = f.Step(context.Background())
	require.NoError(t, err)
	st, err = store.Status()
	require.NoError(t, err)
	require.Equal(t, int64(6), st.AggregatesFrom, "the baseline is taken again at the next block")
}

// outageChain is a chain whose follower ran to block 4, stopped, and resumes when the node has
// pruned every state below 11: epochs close at heights 3, 6, 9, 12 and 15.
func outageChain(t *testing.T, to int64) (*fakeChain, *Store, *Follower, string) {
	t.Helper()
	chain, _ := econChain(t)
	addEconBlocks(t, chain, 1, 4)
	dir := t.TempDir()
	store, f := newPrunedFollower(t, chain, dir, 1)
	_, err := f.Step(context.Background())
	require.NoError(t, err)
	addEconBlocks(t, chain, 5, to)
	chain.prunedBelow = 11
	return chain, store, f, dir
}

func TestEpochs_anOutageOverEpochClosesMarksThemUnavailableAndTheNextIsExact(t *testing.T) {
	chain, store, f, _ := outageChain(t, 16)
	defer store.Close()

	_, err := f.Step(context.Background())
	require.NoError(t, err, "epoch closes whose state is pruned do not stop the index")

	e1 := epochOf(t, store, 1)
	require.False(t, e1.Unavailable, "epoch 1 closed before the outage")
	require.Equal(t, "600", e1.MintedValidators)
	for _, epoch := range []uint64{2, 3} {
		e := epochOf(t, store, epoch)
		require.True(t, e.Unavailable, "epoch %d closed at a height whose state is gone", epoch)
		require.Contains(t, e.UnavailableReason, "no longer kept the chain state")
		require.Empty(t, e.MintedTotal, "no figure is made up")
		require.Empty(t, e.Burned)
		require.Equal(t, int64(3*(epoch-1)+1), e.StartHeight, "the heights are the rule's, exact")
		require.Equal(t, int64(3*epoch), e.EndHeight)
	}

	// Epoch 4 closed at 12, whose state is there, but its start (the end of epoch 3) is not: its
	// change in the chain's totals would be the change over epochs 3 and 4.
	e4 := epochOf(t, store, 4)
	require.True(t, e4.Unavailable)
	require.Contains(t, e4.UnavailableReason, "end of the epoch before")
	require.Empty(t, e4.MintedTotal)
	points, err := store.Supply(1, 10)
	require.NoError(t, err)
	require.Len(t, points, 3, "epochs 1, 4 and 5: epochs 2 and 3 have no supply point")
	require.Equal(t, uint64(4), points[1].Epoch, "the supply at height 12 is exact")
	require.Equal(t, int64(12), points[1].Height)
	rows, found, err := store.EpochValidators(4, 1, 10)
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, rows, 3)
	require.Equal(t, uint64(3), rows[0].SignedBlocks+rows[0].MissedBlocks, "the follower counted every block of the epoch")
	for _, epoch := range []uint64{2, 3} {
		rows, found, err := store.EpochValidators(epoch, 1, 10)
		require.NoError(t, err)
		require.True(t, found, "the epoch is known")
		require.Empty(t, rows, "its validators' state was not readable")
	}

	e5 := epochOf(t, store, 5)
	require.False(t, e5.Unavailable, "an epoch that began at a readable close is exact again")
	require.Equal(t, int64(13), e5.StartHeight)
	require.Equal(t, "600", e5.MintedValidators)
	require.Equal(t, "6", e5.Burned, "three blocks at 2 each: never the change over several epochs")
	require.Equal(t, "15", e5.FeesCollected)

	for k := range dump(t, store) {
		require.False(t, strings.HasPrefix(k, pfxCounter+string(be64(2))) || strings.HasPrefix(k, pfxCounter+string(be64(3))),
			"the tallies of an unavailable epoch are dropped, not left behind")
	}
	require.NotEmpty(t, chain.asked)
}

func TestEpochs_anOutageEndingBeforeAReadableCloseLeavesTheLatestValidatorsAvailable(t *testing.T) {
	_, store, f, _ := outageChain(t, 10)
	defer store.Close()

	_, err := f.Step(context.Background())
	require.NoError(t, err)
	require.True(t, epochOf(t, store, 3).Unavailable)

	api := NewAPI(store, fixedTip{earliest: 1, latest: 10})
	code, body := get(t, api, http.MethodGet, "/index/v1/epochs/3")
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, true, body["unavailable"])
	require.NotEmpty(t, body["unavailable_reason"])
	require.NotContains(t, body, "minted_total", "a figure that is not known is not shown as zero")
	require.Equal(t, 7.0, body["start_height"])
	require.Equal(t, 9.0, body["end_height"])

	code, body = get(t, api, http.MethodGet, "/index/v1/epochs/1")
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, false, body["unavailable"])
	require.Equal(t, "680", body["minted_total"])

	rows := list(t, api, "/index/v1/epochs/3/validators", "validators")
	require.Empty(t, rows)
	vals := list(t, api, "/index/v1/validators", "validators")
	require.Len(t, vals, 3, "the newest epoch that has validators, not an empty page")
	require.Equal(t, 1.0, vals[0].(map[string]any)["last_epoch"].(map[string]any)["epoch"])
}

func TestEpochs_anOutageAndAFullRunAgreeOnTheEpochsBeforeIt(t *testing.T) {
	whole, _ := econChain(t)
	addEconBlocks(t, whole, 1, 16)
	wholeStore, _ := follow(t, whole, t.TempDir(), 1)
	defer wholeStore.Close()
	_, store, f, _ := outageChain(t, 16)
	defer store.Close()
	_, err := f.Step(context.Background())
	require.NoError(t, err)

	require.Equal(t, epochOf(t, wholeStore, 1), epochOf(t, store, 1))
	require.Equal(t, epochOf(t, wholeStore, 5).Burned, epochOf(t, store, 5).Burned, "the exact epochs have the chain's own figures")
}

func TestEpochs_aFailureThatIsNotAPrunedStateStillStopsTheEpochClose(t *testing.T) {
	chain, store, f, _ := outageChain(t, 7)
	defer store.Close()
	// Block 6 closes epoch 2 and its state is pruned; the supply read fails some other way at 6.
	chain.prunedBelow = 0
	boom := errors.New("connection reset by peer")
	chain.failing = map[string]error{queryKey(querySupplyOf, 6): boom}

	_, err := f.Step(context.Background())
	require.ErrorIs(t, err, boom)
	st, err := store.Status()
	require.NoError(t, err)
	require.Equal(t, int64(5), st.Cursor, "the block is not indexed and the epoch is not recorded")
	_, ok, err := store.Epoch(2)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestEpochs_aPrunedStateInTheMiddleOfAClosesLeavesNothingOfTheEpochButItsUnavailableRow(t *testing.T) {
	chain, store, f, _ := outageChain(t, 7)
	defer store.Close()
	// The node prunes while the epoch closes: the emission state at 6 is still there, the supply is not.
	chain.prunedBelow = 0
	chain.failing = map[string]error{queryKey(querySupplyOf, 6): prunedStateError(querySupplyOf, 6)}

	_, err := f.Step(context.Background())
	require.NoError(t, err)
	e2 := epochOf(t, store, 2)
	require.True(t, e2.Unavailable)
	points, err := store.Supply(1, 10)
	require.NoError(t, err)
	require.Len(t, points, 1, "epoch 1's point only: no half-written point for epoch 2")
	rows, found, err := store.EpochValidators(2, 1, 10)
	require.NoError(t, err)
	require.True(t, found)
	require.Empty(t, rows)
}

func TestStatus_aStuckFollowerSaysWhy(t *testing.T) {
	chain, _ := econChain(t)
	addEconBlocks(t, chain, 1, 6)
	boom := errors.New("connection reset by peer")
	chain.failing = map[string]error{queryKey(queryEmissionState, 1): boom}
	store, f := newPrunedFollower(t, chain, t.TempDir(), 1)
	defer store.Close()
	api := NewAPI(store, fixedTip{earliest: 1, latest: 6})

	for i := 0; i < 2; i++ {
		_, err := f.Step(context.Background())
		require.ErrorIs(t, err, boom)
	}
	code, body := get(t, api, http.MethodGet, "/index/v1/operator/status")
	require.Equal(t, http.StatusOK, code)
	stuck, ok := body["stuck"].(map[string]any)
	require.True(t, ok, "the status names the stuck follower: %v", body)
	require.Equal(t, 1.0, stuck["height"], "the block it cannot index")
	require.Contains(t, stuck["reason"], "connection reset by peer")
	require.Equal(t, false, stuck["state_pruned"])
	require.Equal(t, 2.0, stuck["failures"])
	require.NotEmpty(t, stuck["since"])
	require.Equal(t, 0.0, body["cursor"])

	delete(chain.failing, queryKey(queryEmissionState, 1))
	_, err := f.Step(context.Background())
	require.NoError(t, err)
	_, body = get(t, api, http.MethodGet, "/index/v1/status")
	require.NotContains(t, body, "stuck", "a pass that works clears it")
	require.Equal(t, 6.0, body["cursor"])
}

func TestStatus_aPrunedStateThatBlocksABlockIsSaidToBePruned(t *testing.T) {
	chain, _ := econChain(t)
	addEconBlocks(t, chain, 1, 4)
	store, f := newPrunedFollower(t, chain, t.TempDir(), 1)
	defer store.Close()
	_, err := f.Step(context.Background())
	require.NoError(t, err)
	addEconBlocks(t, chain, 5, 5)
	// Block 5 carries no state query of its own, so only a lookup the aggregates cannot do
	// without can fail here: a validator that unjails and is not in the registry.
	chain.prunedBelow = 100
	stranger := newFakeValidator(t, 9, 5)
	chain.ledger.validators = append(chain.ledger.validators, stranger)
	unjail := okTx(t, one(&slashingtypes.MsgUnjail{ValidatorAddr: stranger.operator}), one(&slashingtypes.MsgUnjailResponse{}), addr(t, 6))
	putTx(chain, 5, unjail)

	_, err = f.Step(context.Background())
	require.Error(t, err)
	require.True(t, isStatePruned(err))
	st, err := store.Status()
	require.NoError(t, err)
	require.NotNil(t, st.Stuck)
	require.True(t, st.Stuck.StatePruned, "the status tells a state no retry brings back from a failure that may pass")
	require.Equal(t, int64(5), st.Stuck.Height)
	require.Equal(t, int64(4), st.Cursor)
}
