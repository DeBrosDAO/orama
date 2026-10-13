package keeper_test

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"

	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/shielded/bundle"
	"github.com/DeBrosOfficial/network/chain/x/shielded/keeper"
	"github.com/DeBrosOfficial/network/chain/x/shielded/testutil"
	"github.com/DeBrosOfficial/network/chain/x/shielded/types"
)

// seedTree puts n made-up commitments in the tree without going through a bundle, so a test can
// stand a few leaves short of a subtree boundary.
func seedTree(t *testing.T, e *testutil.Env, n uint64) {
	t.Helper()
	cmx := make([][bundle.NodeLen]byte, n)
	for i := range cmx {
		cmx[i][0], cmx[i][1], cmx[i][2] = 0x07, byte(i), byte(i>>8)
	}
	frontier, root, err := testutil.Tree{}.Append(nil, cmx)
	require.NoError(t, err)
	require.NoError(t, e.Keeper.Frontier.Set(e.Ctx, frontier))
	require.NoError(t, e.Keeper.CurrentRoot.Set(e.Ctx, root[:]))
	require.NoError(t, e.Keeper.TreeSize.Set(e.Ctx, n))
}

func subtreeEvents(e *testutil.Env) []sdk.Event {
	var out []sdk.Event
	for _, ev := range e.Ctx.EventManager().Events() {
		if ev.Type == types.EventTypeSubtreeComplete {
			out = append(out, ev)
		}
	}
	return out
}

func attr(ev sdk.Event, key string) string {
	for _, a := range ev.Attributes {
		if a.Key == key {
			return a.Value
		}
	}
	return ""
}

// shieldActions shields a bundle of n actions; the signer also funds the n nullifier fees.
func shieldActions(t *testing.T, e *testutil.Env, seed byte, n int) {
	t.Helper()
	e.Bank.Fund(alice.String(), 1000+int64(n))
	raw := testutil.Bundle{Seed: seed, ValueBalance: -1000, Anchor: emptyAnchor(t)}.Encode(n)
	_, err := keeper.NewMsgServerImpl(e.Keeper).Shield(e.Ctx, &types.MsgShield{Signer: alice.String(), Bundle: raw})
	require.NoError(t, err)
}

// The tx that fills a subtree stores a root and reports it, which the tx before it did not do; its
// gas limit comes from a simulation of that earlier state, so the write and the event are charged on
// an unmetered context and cost the tx's meter nothing.
func TestEmitSubtree_costsTheTransactionNoGas(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	seedTree(t, e, types.SubtreeLeaves)
	frontier, err := e.Keeper.Frontier.Get(e.Ctx)
	require.NoError(t, err)

	meter := storetypes.NewGasMeter(1_000_000_000)
	require.NoError(t, keeper.EmitSubtreeForTest(e.Ctx.WithGasMeter(meter), e.Keeper, frontier, 0))

	require.Zero(t, meter.GasConsumed(), "the subtree write and event are not charged to the transaction")
	got, err := e.Keeper.Subtrees.Get(e.Ctx, 0)
	require.NoError(t, err)
	require.Equal(t, uint64(0), got.Index)
	require.Len(t, subtreeEvents(e), 1)
}

// A bundle that straddles the end of a subtree reports that subtree's root, computed from the
// frontier as it stood when the subtree filled (before the bundle's later leaves), and the tree
// still ends up holding every leaf.
func TestAppendCommitments_aBundleThatFillsASubtreeEmitsItsRoot(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	seedTree(t, e, types.SubtreeLeaves-2)

	shieldActions(t, e, 3, 4)

	evs := subtreeEvents(e)
	require.Len(t, evs, 1)
	require.Equal(t, "0", attr(evs[0], types.AttributeSubtreeIndex))
	size, err := e.Keeper.TreeSize.Get(e.Ctx)
	require.NoError(t, err)
	require.Equal(t, types.SubtreeLeaves+2, size)

	// The test tree's frontier is the leaves themselves, so the reported root is the hash of the
	// first 2^16 leaves, the subtree as it was at the boundary, not of the leaves after it.
	frontier, err := e.Keeper.Frontier.Get(e.Ctx)
	require.NoError(t, err)
	want := sha256.Sum256(frontier[:int(types.SubtreeLeaves)*bundle.NodeLen])
	require.Equal(t, hex.EncodeToString(want[:]), attr(evs[0], types.AttributeSubtreeRoot))
	root, err := e.Keeper.CurrentRoot.Get(e.Ctx)
	require.NoError(t, err)
	wantRoot := sha256.Sum256(frontier)
	require.Equal(t, wantRoot[:], root, "the current root is the root after the whole bundle")
}

// A bundle whose last leaf is a subtree's last leaf reports it too, and the second subtree is
// index 1.
func TestAppendCommitments_theLastLeafOfASubtreeCountsAndIndicesCount(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	seedTree(t, e, 2*types.SubtreeLeaves-1)

	shieldActions(t, e, 4, 1)

	evs := subtreeEvents(e)
	require.Len(t, evs, 1)
	require.Equal(t, "1", attr(evs[0], types.AttributeSubtreeIndex))
	size, err := e.Keeper.TreeSize.Get(e.Ctx)
	require.NoError(t, err)
	require.Equal(t, 2*types.SubtreeLeaves, size)
}

// Bundles that stay inside a subtree emit nothing, including the one that starts right after a
// boundary.
func TestAppendCommitments_noEventWithoutAFilledSubtree(t *testing.T) {
	for _, start := range []uint64{0, 10, types.SubtreeLeaves, types.SubtreeLeaves + 5} {
		e := testutil.NewEnv(t, nil)
		if start > 0 {
			seedTree(t, e, start)
		}
		shieldActions(t, e, 5, 2)
		require.Empty(t, subtreeEvents(e), "tree of %d leaves plus 2", start)
		size, err := e.Keeper.TreeSize.Get(e.Ctx)
		require.NoError(t, err)
		require.Equal(t, start+2, size)
	}
}

// TreeFrontier answers the stored frontier with the size and root TreeState reports, on an empty
// tree (no frontier bytes at all) and after a bundle.
func TestTreeFrontier_agreesWithTreeState(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	q := keeper.NewQueryServerImpl(e.Keeper)

	check := func(wantSize uint64) *types.QueryTreeFrontierResponse {
		state, err := q.TreeState(e.Ctx, &types.QueryTreeStateRequest{})
		require.NoError(t, err)
		got, err := q.TreeFrontier(e.Ctx, &types.QueryTreeFrontierRequest{})
		require.NoError(t, err)
		require.Equal(t, wantSize, got.TreeSize)
		require.Equal(t, state.TreeSize, got.TreeSize)
		require.Equal(t, state.CurrentRoot, got.CurrentRoot, "the frontier's root is the tree state's")
		require.Equal(t, uint64(e.Ctx.BlockHeight()), got.Height)
		return got
	}

	empty := check(0)
	require.Empty(t, empty.Frontier)

	shieldActions(t, e, 6, 3)
	full := check(3)
	stored, err := e.Keeper.Frontier.Get(e.Ctx)
	require.NoError(t, err)
	require.Equal(t, stored, full.Frontier)
	require.NotEmpty(t, full.Frontier)
}
