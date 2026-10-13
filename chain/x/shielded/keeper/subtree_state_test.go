package keeper_test

import (
	"encoding/hex"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/shielded/bundle"
	"github.com/DeBrosOfficial/network/chain/x/shielded/keeper"
	"github.com/DeBrosOfficial/network/chain/x/shielded/testutil"
	"github.com/DeBrosOfficial/network/chain/x/shielded/types"
)

func bundleEvents(e *testutil.Env) []sdk.Event {
	var out []sdk.Event
	for _, ev := range e.Ctx.EventManager().Events() {
		if ev.Type == types.EventTypeBundle {
			out = append(out, ev)
		}
	}
	return out
}

// storeRoots writes a run of made-up subtree roots and sets the tree size to match, for the tests
// that page through them or break the run.
func storeRoots(t *testing.T, e *testutil.Env, n uint64) {
	t.Helper()
	for i := uint64(0); i < n; i++ {
		root := make([]byte, bundle.NodeLen)
		root[0], root[1] = 0xa0, byte(i)
		require.NoError(t, e.Keeper.Subtrees.Set(e.Ctx, i, types.SubtreeRoot{Index: i, Root: root, Height: int64(10 + i)}))
	}
	require.NoError(t, e.Keeper.TreeSize.Set(e.Ctx, n*types.SubtreeLeaves+3))
}

// Every accepted bundle reports the size the tree had before its append and how many commitments it
// appended, whether the bundle straddles a subtree boundary or not, so an indexer places each leaf
// from events alone.
func TestBundleEvent_carriesTheTreeSizeBeforeAndTheCommitmentCount(t *testing.T) {
	for _, start := range []uint64{0, 10, types.SubtreeLeaves - 2} {
		e := testutil.NewEnv(t, nil)
		if start > 0 {
			seedTree(t, e, start)
		}
		shieldActions(t, e, 3, 4)
		evs := bundleEvents(e)
		require.Len(t, evs, 1)
		require.Equal(t, "shield", attr(evs[0], types.AttributeKind))
		require.Equal(t, "4", attr(evs[0], types.AttributeActions))
		require.Equal(t, "4", attr(evs[0], types.AttributeCommitments))
		require.Equal(t, uitoa(start), attr(evs[0], types.AttributeTreeSizeBefore), "tree of %d leaves", start)

		shieldActions(t, e, 4, 2)
		evs = bundleEvents(e)
		require.Len(t, evs, 2)
		require.Equal(t, uitoa(start+4), attr(evs[1], types.AttributeTreeSizeBefore), "the second bundle follows the first")
		require.Equal(t, "2", attr(evs[1], types.AttributeCommitments))
	}
}

func uitoa(n uint64) string { return strconv.FormatUint(n, 10) }

// The root a filled subtree reports is kept in state with the height that filled it, equal to the
// event's, and the invariant sees a root for every filled subtree.
func TestAppendCommitments_storesTheRootItEmits(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	e.Ctx = e.Ctx.WithBlockHeight(42)
	seedTree(t, e, types.SubtreeLeaves-2)

	shieldActions(t, e, 3, 4)

	got, err := e.Keeper.Subtrees.Get(e.Ctx, 0)
	require.NoError(t, err)
	evs := subtreeEvents(e)
	require.Len(t, evs, 1)
	require.Equal(t, hex.EncodeToString(got.Root), attr(evs[0], types.AttributeSubtreeRoot))
	require.Equal(t, uint64(0), got.Index)
	require.Equal(t, int64(42), got.Height)
	has, err := e.Keeper.Subtrees.Has(e.Ctx, 1)
	require.NoError(t, err)
	require.False(t, has, "only the filled subtree has a root")

	inv, err := e.Keeper.CheckLedgers(e.Ctx)
	require.NoError(t, err)
	require.True(t, inv.SubtreeRootsMatch, inv.Detail)
}

// Bundles that fill nothing store nothing; one that ends on a boundary stores the subtree it closed.
func TestAppendCommitments_storesARootOnlyForAFilledSubtree(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	shieldActions(t, e, 5, 2)
	has, err := e.Keeper.Subtrees.Has(e.Ctx, 0)
	require.NoError(t, err)
	require.False(t, has)

	e = testutil.NewEnv(t, nil)
	seedTree(t, e, 2*types.SubtreeLeaves-1)
	shieldActions(t, e, 4, 1)
	has, err = e.Keeper.Subtrees.Has(e.Ctx, 1)
	require.NoError(t, err)
	require.True(t, has, "the last leaf of a subtree fills it")
}

// SubtreeRoots pages in index order, reports how many subtrees have filled, answers a start past the
// end with an empty page and refuses a limit over the bound.
func TestSubtreeRoots_pagesAndBounds(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	storeRoots(t, e, 5)
	q := keeper.NewQueryServerImpl(e.Keeper)
	page := func(start uint64, limit uint32) *types.QuerySubtreeRootsResponse {
		resp, err := q.SubtreeRoots(e.Ctx, &types.QuerySubtreeRootsRequest{StartIndex: start, Limit: limit})
		require.NoError(t, err)
		require.Equal(t, uint64(5), resp.Total, "start %d limit %d", start, limit)
		require.NotNil(t, resp.Roots)
		return resp
	}

	all := page(0, 0)
	require.Len(t, all.Roots, 5, "no limit means the default, 100")
	for i, r := range all.Roots {
		require.Equal(t, uint64(i), r.Index)
		require.Equal(t, int64(10+i), r.Height)
		require.Len(t, r.Root, bundle.NodeLen)
	}

	var walked []uint64
	for start := uint64(0); ; {
		p := page(start, 2)
		require.LessOrEqual(t, len(p.Roots), 2)
		if len(p.Roots) == 0 {
			break
		}
		for _, r := range p.Roots {
			walked = append(walked, r.Index)
		}
		start += uint64(len(p.Roots))
	}
	require.Equal(t, []uint64{0, 1, 2, 3, 4}, walked)

	require.Len(t, page(3, 1000).Roots, 2)
	require.Empty(t, page(5, 10).Roots, "caught up")
	require.Empty(t, page(1<<62, 10).Roots, "far past the end")

	_, err := q.SubtreeRoots(e.Ctx, &types.QuerySubtreeRootsRequest{Limit: types.SubtreeRootsMaxLimit + 1})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestSubtreeRoots_emptyTreeHasNone(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	resp, err := keeper.NewQueryServerImpl(e.Keeper).SubtreeRoots(e.Ctx, &types.QuerySubtreeRootsRequest{})
	require.NoError(t, err)
	require.Empty(t, resp.Roots)
	require.Zero(t, resp.Total)
}

// A stored root missing under a tree that says it filled is an internal error, not a short page.
func TestSubtreeRoots_aMissingRootIsAnError(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	require.NoError(t, e.Keeper.TreeSize.Set(e.Ctx, 2*types.SubtreeLeaves))
	_, err := keeper.NewQueryServerImpl(e.Keeper).SubtreeRoots(e.Ctx, &types.QuerySubtreeRootsRequest{})
	require.Equal(t, codes.Internal, status.Code(err))
}

// The invariant: a root for each filled subtree and none beyond. A tree that grew past a boundary
// without storing the root, a root left over past the tree, and a missing earlier root all show.
func TestCheckLedgers_subtreeRootsMustMatchTheTree(t *testing.T) {
	t.Run("holds on an empty tree and a full run", func(t *testing.T) {
		e := testutil.NewEnv(t, nil)
		inv, err := e.Keeper.CheckLedgers(e.Ctx)
		require.NoError(t, err)
		require.True(t, inv.SubtreeRootsMatch, inv.Detail)
		storeRoots(t, e, 3)
		inv, err = e.Keeper.CheckLedgers(e.Ctx)
		require.NoError(t, err)
		require.True(t, inv.SubtreeRootsMatch, inv.Detail)
	})
	t.Run("a filled subtree without its root", func(t *testing.T) {
		e := testutil.NewEnv(t, nil)
		storeRoots(t, e, 3)
		require.NoError(t, e.Keeper.TreeSize.Set(e.Ctx, 4*types.SubtreeLeaves))
		inv, err := e.Keeper.CheckLedgers(e.Ctx)
		require.NoError(t, err)
		require.False(t, inv.SubtreeRootsMatch)
		require.Contains(t, inv.Detail, "filled 4 subtrees")
	})
	t.Run("a root for a subtree that has not filled", func(t *testing.T) {
		e := testutil.NewEnv(t, nil)
		storeRoots(t, e, 3)
		require.NoError(t, e.Keeper.TreeSize.Set(e.Ctx, 2*types.SubtreeLeaves+9))
		inv, err := e.Keeper.CheckLedgers(e.Ctx)
		require.NoError(t, err)
		require.False(t, inv.SubtreeRootsMatch)
	})
	t.Run("the query reports the same", func(t *testing.T) {
		e := testutil.NewEnv(t, nil)
		storeRoots(t, e, 1)
		require.NoError(t, e.Keeper.TreeSize.Set(e.Ctx, 3*types.SubtreeLeaves))
		resp, err := keeper.NewQueryServerImpl(e.Keeper).Invariants(e.Ctx, &types.QueryInvariantsRequest{})
		require.NoError(t, err)
		require.False(t, resp.SubtreeRootsMatch)
	})
}

// The roots are genesis state: an export carries them in index order, an import restores them, and
// a list that is not exactly one root per filled subtree is refused.
func TestGenesis_subtreeRootsRoundTripAndValidate(t *testing.T) {
	src := testutil.NewEnv(t, nil)
	seedTree(t, src, types.SubtreeLeaves-2)
	shieldActions(t, src, 3, 4)
	src.EndBlock()
	exported, err := src.Keeper.ExportGenesis(src.Ctx)
	require.NoError(t, err)
	require.Len(t, exported.SubtreeRoots, 1)
	require.Equal(t, uint64(types.SubtreeLeaves+2), exported.TreeSize)
	// The test tree's frontier is its leaves, longer than a real one; what is checked here is the roots.
	exported.Frontier = exported.Frontier[:100]
	require.NoError(t, exported.Validate())

	dst := testutil.NewEnv(t, func(gs *types.GenesisState) { *gs = *exported })
	again, err := dst.Keeper.ExportGenesis(dst.Ctx)
	require.NoError(t, err)
	require.Equal(t, exported.SubtreeRoots, again.SubtreeRoots)
	inv, err := dst.Keeper.CheckLedgers(dst.Ctx)
	require.NoError(t, err)
	require.True(t, inv.SubtreeRootsMatch, inv.Detail)

	good := exported.SubtreeRoots[0]
	for name, mutate := range map[string]func(gs *types.GenesisState){
		"a root missing": func(gs *types.GenesisState) { gs.SubtreeRoots = nil },
		"a root too many": func(gs *types.GenesisState) {
			gs.SubtreeRoots = append(gs.SubtreeRoots, types.SubtreeRoot{Index: 1, Root: good.Root})
		},
		"the wrong index": func(gs *types.GenesisState) {
			gs.SubtreeRoots = []types.SubtreeRoot{{Index: 1, Root: good.Root, Height: 1}}
		},
		"a short root": func(gs *types.GenesisState) {
			gs.SubtreeRoots = []types.SubtreeRoot{{Index: 0, Root: good.Root[:31], Height: 1}}
		},
		"a negative height": func(gs *types.GenesisState) {
			gs.SubtreeRoots = []types.SubtreeRoot{{Index: 0, Root: good.Root, Height: -1}}
		},
		"roots on a small tree": func(gs *types.GenesisState) { gs.TreeSize = 10 },
	} {
		gs := *exported
		gs.SubtreeRoots = append([]types.SubtreeRoot(nil), exported.SubtreeRoots...)
		mutate(&gs)
		require.Error(t, gs.Validate(), name)
	}
}
