package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	"github.com/DeBrosOfficial/network/chain/x/shielded/bundle"
	"github.com/DeBrosOfficial/network/chain/x/shielded/nullifier"
	"github.com/DeBrosOfficial/network/chain/x/shielded/pool"
	"github.com/DeBrosOfficial/network/chain/x/shielded/testutil"
	"github.com/DeBrosOfficial/network/chain/x/shielded/types"
)

// busyEnv has shielded value, a transfer, a top-up and a queued bond.
func busyEnv(t *testing.T) *testutil.Env {
	t.Helper()
	e := capEnv(t, nil)
	e.Fees.Proposer = bob
	require.NoError(t, transfer(e, transferBundle(20, 15, emptyRoot())))
	_, err := unshield(t, e, topup(2, 47, alice))
	require.NoError(t, err)
	resp, err := unshield(t, e, bond(3, 41, alice))
	require.NoError(t, err)
	require.True(t, resp.Queued)
	e.EndBlock()
	return e
}

func TestGenesis_exportThenInitRoundTrips(t *testing.T) {
	src := busyEnv(t)
	exported, err := src.Keeper.ExportGenesis(src.Ctx)
	require.NoError(t, err)
	require.NoError(t, exported.Validate())
	require.NotEmpty(t, exported.Pools)
	require.NotEmpty(t, exported.Queue)
	require.NotEmpty(t, exported.Anchors)
	require.Equal(t, uint64(4), exported.NullifierCount)
	require.Len(t, exported.Nullifiers, 4)

	dst := testutil.NewEnv(t, func(gs *types.GenesisState) { *gs = *exported })
	again, err := dst.Keeper.ExportGenesis(dst.Ctx)
	require.NoError(t, err)
	require.Equal(t, exported, again)

	for _, raw := range exported.Nullifiers {
		var nf [bundle.NodeLen]byte
		copy(nf[:], raw)
		spent, err := dst.Store.Spent(nf, dst.Height)
		require.NoError(t, err)
		require.True(t, spent, "%x must be spent again after an import", nf)
	}
	inv, err := dst.Keeper.CheckInvariants(dst.Ctx)
	require.NoError(t, err)
	require.True(t, inv.AccumulatorMatches, inv.Detail)
}

func TestGenesis_aRestoredChainRefusesAReplayedNullifier(t *testing.T) {
	src := busyEnv(t)
	exported, err := src.Keeper.ExportGenesis(src.Ctx)
	require.NoError(t, err)
	dst := testutil.NewEnv(t, func(gs *types.GenesisState) { *gs = *exported })
	dst.Bank.Fund(alice.String(), 1000)
	_, err = unshield(t, dst, topup(2, 11, alice))
	require.ErrorIs(t, err, types.ErrNullifierSpent, "nullifier 2 was spent before the export")
}

func TestGenesis_aNonEmptyNullifierDatabaseIsRefused(t *testing.T) {
	e2 := testutil.NewEnv(t, nil)
	require.NoError(t, e2.Store.Commit(1, [][bundle.NodeLen]byte{testutil.Nullifier(1, 0)}))
	err := e2.Keeper.InitGenesis(e2.Ctx, *types.DefaultGenesisState())
	require.ErrorIs(t, err, types.ErrNullifierStore)
}

func TestGenesis_exportRefusesADatabaseThatDisagreesWithState(t *testing.T) {
	e := busyEnv(t)
	require.NoError(t, e.Keeper.NullifierCount.Set(e.Ctx, 99))
	_, err := e.Keeper.ExportGenesis(e.Ctx)
	require.ErrorIs(t, err, types.ErrNullifierStore)
}

func TestGenesis_exportIgnoresRecordsOfABlockThatNeverCommitted(t *testing.T) {
	e := busyEnv(t)
	require.NoError(t, e.Store.Commit(e.Height+5, [][bundle.NodeLen]byte{testutil.Nullifier(99, 0)}))
	exported, err := e.Keeper.ExportGenesis(e.Ctx)
	require.NoError(t, err)
	require.Len(t, exported.Nullifiers, 4)
	require.NoError(t, exported.Validate())
}

func TestGenesis_validate(t *testing.T) {
	good := func() types.GenesisState { return *types.DefaultGenesisState() }
	native := pool.NativeAsset[:]
	nf := func(b byte) []byte { return append([]byte{b}, make([]byte, 31)...) }
	var acc [bundle.NodeLen]byte
	acc = nullifier.Fold(acc, [bundle.NodeLen]byte(nf(1)))

	cases := map[string]func(*types.GenesisState){
		"default is valid":   nil,
		"zero anchor window": func(g *types.GenesisState) { g.Params.AnchorWindowBlocks = 0 },
		"zero action gas":    func(g *types.GenesisState) { g.Params.ActionGas = 0 },
		"non-positive floor": func(g *types.GenesisState) { g.Params.UnshieldFloor = math.ZeroInt() },
		"another asset": func(g *types.GenesisState) {
			g.Pools = []types.PoolBalance{{Vintage: 1, Asset: make([]byte, 32), Balance: math.OneInt()}}
		},
		"negative pool": func(g *types.GenesisState) {
			g.Pools = []types.PoolBalance{{Vintage: 1, Asset: native, Balance: math.NewInt(-1)}}
		},
		"duplicate pool": func(g *types.GenesisState) {
			p := types.PoolBalance{Vintage: 1, Asset: native, Balance: math.OneInt()}
			g.Pools = []types.PoolBalance{p, p}
		},
		"limiter without a pool": func(g *types.GenesisState) {
			g.Limiters = []types.Limiter{{Vintage: 1, Asset: native, Counted: math.ZeroInt()}}
		},
		"queue id at next": func(g *types.GenesisState) {
			g.Queue = []types.QueuedUnshield{{Id: 3, Asset: native, Owner: alice.String(), Target: types.UnshieldTargetBond, Amount: math.OneInt()}}
			g.NextQueueId = 3
		},
		"queued fee top-up": func(g *types.GenesisState) {
			g.Queue = []types.QueuedUnshield{{Id: 0, Asset: native, Owner: alice.String(), Target: types.UnshieldTargetFeeTopup, Amount: math.OneInt()}}
			g.NextQueueId = 1
		},
		"frontier without size":       func(g *types.GenesisState) { g.Frontier = []byte{1} },
		"accumulator of wrong length": func(g *types.GenesisState) { g.NullifierAccumulator = []byte{1} },
		"count without nullifiers":    func(g *types.GenesisState) { g.NullifierCount = 1 },
		"accumulator that does not fold": func(g *types.GenesisState) {
			g.NullifierCount = 1
			g.Nullifiers = [][]byte{nf(1)}
		},
		"duplicate nullifier": func(g *types.GenesisState) {
			g.NullifierCount = 2
			g.Nullifiers = [][]byte{nf(1), nf(1)}
		},
		"short nullifier": func(g *types.GenesisState) {
			g.NullifierCount = 1
			g.Nullifiers = [][]byte{{1, 2}}
		},
		"a nullifier that folds": func(g *types.GenesisState) {
			g.NullifierCount = 1
			g.NullifierAccumulator = acc[:]
			g.Nullifiers = [][]byte{nf(1)}
		},
	}
	valid := map[string]bool{"default is valid": true, "a nullifier that folds": true}
	for name, mutate := range cases {
		g := good()
		if mutate != nil {
			mutate(&g)
		}
		err := g.Validate()
		if valid[name] {
			require.NoError(t, err, name)
		} else {
			require.Error(t, err, name)
		}
	}
}

func TestCheckNullifierStore_refusesADatabaseThatDisagreesWithState(t *testing.T) {
	e := busyEnv(t)
	require.NoError(t, e.Keeper.CheckNullifierStore(e.Ctx))
	require.NoError(t, e.Store.Reset(), "a restore that lost the nullifier database")
	require.ErrorIs(t, e.Keeper.CheckNullifierStore(e.Ctx), types.ErrNullifierStore)
}
