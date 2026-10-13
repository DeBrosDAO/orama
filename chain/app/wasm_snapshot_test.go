//go:build cgo && !nowasm

package app_test

import (
	"path/filepath"
	"testing"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/log/v2"

	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/store/v2/snapshots"
	snapshottypes "github.com/cosmos/cosmos-sdk/store/v2/snapshots/types"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"

	"github.com/DeBrosOfficial/network/chain/app"
	"github.com/DeBrosOfficial/network/chain/contracts/standard"
)

const (
	snapshotTestInterval   = 5
	snapshotTestKeepRecent = 2
	snapshotTestTimeout    = 30 * time.Second
	snapshotTestPoll       = 50 * time.Millisecond
)

// newSnapshotApp builds an app that owns a real home directory and a snapshot store, the way a node
// does. Contract bytecode lives under <home>/wasm, outside the IAVL tree, so the home is what the
// state-sync round trip has to carry the bytecode into.
func newSnapshotApp(t *testing.T) *app.OramaApp {
	t.Helper()
	app.SetAddressPrefixes()
	home := t.TempDir()
	snapshotDir := filepath.Join(home, "data", "snapshots")
	store, err := snapshots.NewStore(dbm.NewMemDB(), snapshotDir)
	require.NoError(t, err)
	oramaApp := app.NewOramaApp(
		log.NewNopLogger(), dbm.NewMemDB(), true,
		simtestutil.AppOptionsMap{flags.FlagHome: home},
		baseapp.SetChainID(testChainID),
		baseapp.SetSnapshot(store, snapshottypes.NewSnapshotOptions(snapshotTestInterval, snapshotTestKeepRecent)),
	)
	t.Cleanup(func() { require.NoError(t, oramaApp.Close()) })
	return oramaApp
}

// TestStateSync_restoresContractBytecode guards the wasm snapshot extension. A state snapshot of
// the IAVL stores carries each code's CodeInfo but not its bytecode, which wasmd keeps on disk. A
// node that state-synced without the extension therefore knew its contracts and could not run them,
// and diverged at the first transaction that called one. This restores a snapshot into an empty
// node with an empty home and checks that every standard contract's bytecode arrives intact.
func TestStateSync_restoresContractBytecode(t *testing.T) {
	source := newSnapshotApp(t)
	genesisTime := time.Unix(1_700_000_000, 0)
	genState, _ := committeeGenesis(t, source, 1, 365)
	require.NoError(t, standard.Apply(genState))
	manifest, err := standard.Load()
	require.NoError(t, err)
	require.NotEmpty(t, manifest.Contracts)
	initChain(t, source, genState, 100_000_000, genesisTime)

	var snapshotAppHash []byte
	for height := int64(1); height <= snapshotTestInterval; height++ {
		resp := finalize(t, source, height, genesisTime.Add(time.Duration(height)*2*time.Second))
		snapshotAppHash = resp.AppHash
	}
	require.Eventually(t, func() bool {
		list, err := source.SnapshotManager().List()
		return err == nil && len(list) > 0
	}, snapshotTestTimeout, snapshotTestPoll, "no snapshot was taken at height %d", snapshotTestInterval)

	offered, err := source.ListSnapshots(&abci.RequestListSnapshots{})
	require.NoError(t, err)
	require.Len(t, offered.Snapshots, 1)
	snapshot := offered.Snapshots[0]
	require.EqualValues(t, snapshotTestInterval, snapshot.Height)
	require.NotZero(t, snapshot.Chunks)

	target := newSnapshotApp(t)
	offer, err := target.OfferSnapshot(&abci.RequestOfferSnapshot{Snapshot: snapshot, AppHash: snapshotAppHash})
	require.NoError(t, err)
	require.Equal(t, abci.ResponseOfferSnapshot_ACCEPT, offer.Result)
	for index := uint32(0); index < snapshot.Chunks; index++ {
		chunk, err := source.LoadSnapshotChunk(&abci.RequestLoadSnapshotChunk{
			Height: snapshot.Height, Format: snapshot.Format, Chunk: index,
		})
		require.NoError(t, err)
		applied, err := target.ApplySnapshotChunk(&abci.RequestApplySnapshotChunk{Index: index, Chunk: chunk.Chunk})
		require.NoError(t, err)
		require.Equal(t, abci.ResponseApplySnapshotChunk_ACCEPT, applied.Result, "chunk %d", index)
	}

	info, err := target.Info(&abci.RequestInfo{})
	require.NoError(t, err)
	require.EqualValues(t, snapshot.Height, info.LastBlockHeight)
	require.Equal(t, snapshotAppHash, info.LastBlockAppHash)

	sourceCtx := source.NewContext(true)
	targetCtx := target.NewContext(true)
	for i := range manifest.Contracts {
		codeID := uint64(i + 1)
		want, err := source.WasmKeeper().GetByteCode(sourceCtx, codeID)
		require.NoError(t, err, "source bytecode of code %d", codeID)
		require.NotEmpty(t, want)
		got, err := target.WasmKeeper().GetByteCode(targetCtx, codeID)
		require.NoError(t, err, "restored node has no bytecode for code %d", codeID)
		require.Equal(t, want, got, "restored bytecode of code %d differs", codeID)
	}
}
