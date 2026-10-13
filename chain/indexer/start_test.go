package indexer

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveStart_anOperatorsHeightIsKept(t *testing.T) {
	store := openStore(t, t.TempDir())
	defer store.Close()

	got, err := ResolveStart(context.Background(), fixedTip{earliest: 50, latest: 90}, store, 7)

	require.NoError(t, err)
	require.Equal(t, int64(7), got, "a pruned height is refused when the follower steps, with the message that names it")
}

func TestResolveStart_aNewIndexStartsAtTheNodesEarliestBlock(t *testing.T) {
	store := openStore(t, t.TempDir())
	defer store.Close()
	ctx := context.Background()

	got, err := ResolveStart(ctx, fixedTip{earliest: 50, latest: 90}, store, 0)
	require.NoError(t, err)
	require.Equal(t, int64(50), got)

	got, err = ResolveStart(ctx, fixedTip{earliest: 0, latest: 90}, store, 0)
	require.NoError(t, err)
	require.Equal(t, int64(1), got, "a node that reports no earliest block has the chain from its first")
}

func TestResolveStart_anIndexKeepsItsOwnStart(t *testing.T) {
	chain := newFakeChain()
	chain.add()
	chain.add()
	store := index(t, chain, 2)

	got, err := ResolveStart(context.Background(), fixedTip{earliest: 1, latest: 9}, store, 0)

	require.NoError(t, err)
	require.Equal(t, int64(2), got, "not the node's earliest block, which would be refused as another start")
	_, err = NewFollower(chain, store, got)
	require.NoError(t, err)
}

func TestResolveStart_refusesNegativeAndReportsAnUnreachableNode(t *testing.T) {
	store := openStore(t, t.TempDir())
	defer store.Close()
	ctx := context.Background()

	_, err := ResolveStart(ctx, fixedTip{}, store, -1)
	require.ErrorContains(t, err, "must not be negative")

	_, err = ResolveStart(ctx, fixedTip{err: errors.New("connection refused")}, store, 0)
	require.ErrorContains(t, err, "earliest block")
	require.ErrorContains(t, err, "connection refused")
}

func TestResolveStart_aNodeThatPrunedTheChainsFirstBlocksStillGetsAFreshIndex(t *testing.T) {
	chain := newFakeChain()
	for range 6 {
		chain.add()
	}
	chain.earliest = 4
	store := openStore(t, t.TempDir())
	defer store.Close()

	start, err := ResolveStart(context.Background(), chain, store, 0)
	require.NoError(t, err)
	f, err := NewFollower(chain, store, start)
	require.NoError(t, err)
	n, err := f.Step(context.Background())

	require.NoError(t, err, "starting at block 1 would stop with ErrPruned on every start")
	require.Equal(t, 3, n, "blocks 4 to 6")
	st, err := store.Status()
	require.NoError(t, err)
	require.Equal(t, int64(4), st.StartHeight)
}
