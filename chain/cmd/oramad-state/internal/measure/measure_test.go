package measure

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseSnapshotLine(t *testing.T) {
	var res SnapshotResult
	require.NoError(t, parseSnapshotLine("Exporting snapshot for height 12\nSnapshot created at height 12, format 3, chunks 7\n", &res))
	require.Equal(t, uint64(12), res.Height)
	require.Equal(t, uint32(3), res.Format)
	require.Equal(t, uint32(7), res.Chunks)

	err := parseSnapshotLine("nothing useful", &SnapshotResult{})
	require.ErrorContains(t, err, "printed no height")
}

func TestPatchConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	original := "pprof_laddr = \"localhost:6060\"\naddr_book_strict = true\nallow_duplicate_ip = false\ntimeout_commit = \"5s\"\nother = 1\n"
	require.NoError(t, os.WriteFile(path, []byte(original), 0o600))

	require.NoError(t, patchConfig(path, ""))
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "pprof_laddr = \"\"\naddr_book_strict = false\nallow_duplicate_ip = true\ntimeout_commit = \"5s\"\nother = 1\n", string(got),
		"an empty timeout leaves the file's own")

	require.NoError(t, patchConfig(path, "50ms"))
	got, err = os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(got), "timeout_commit = \"50ms\"")
	require.ErrorContains(t, patchConfig(filepath.Join(t.TempDir(), "absent.toml"), ""), "failed to read")
}

func TestSetConsensusParams(t *testing.T) {
	path := filepath.Join(t.TempDir(), "genesis.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"chain_id":"c","consensus":{"params":{"block":{"max_bytes":"100"}}}}`), 0o600))
	require.NoError(t, setConsensusParams(path))
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, json.Unmarshal(raw, &doc))
	params := doc["consensus"].(map[string]any)["params"].(map[string]any)
	block := params["block"].(map[string]any)
	require.Equal(t, blockMaxGas, block["max_gas"])
	require.Equal(t, "100", block["max_bytes"], "what was there stays")
	require.Equal(t, "0", params["abci"].(map[string]any)["vote_extensions_enable_height"])
	require.Equal(t, "c", doc["chain_id"])
}

func TestSetConsensusParams_createsMissingSections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "genesis.json")
	require.NoError(t, os.WriteFile(path, []byte(`{}`), 0o600))
	require.NoError(t, setConsensusParams(path))
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(raw), blockMaxGas)
	require.Error(t, setConsensusParams(filepath.Join(t.TempDir(), "absent.json")))
}

func TestOramadRun_failureCarriesTheOutput(t *testing.T) {
	sh := Oramad{Bin: "/bin/sh"}
	out, err := sh.Run(context.Background(), "-c", "echo said-this; echo boom >&2; exit 3")
	require.ErrorContains(t, err, "boom")
	require.Contains(t, out.Stdout, "said-this")

	ok, err := sh.Run(context.Background(), "-c", "echo fine")
	require.NoError(t, err)
	require.Equal(t, "fine\n", ok.Stdout)
	require.Positive(t, ok.Elapsed)
}

func TestCheckBinary(t *testing.T) {
	dir := t.TempDir()
	require.ErrorContains(t, Oramad{Bin: filepath.Join(dir, "absent")}.CheckBinary(), "oramad binary")
	require.ErrorContains(t, Oramad{Bin: dir}.CheckBinary(), "not an executable")
	plain := filepath.Join(dir, "plain")
	require.NoError(t, os.WriteFile(plain, []byte("x"), 0o600))
	require.ErrorContains(t, Oramad{Bin: plain}.CheckBinary(), "not an executable")
	require.NoError(t, Oramad{Bin: "/bin/sh"}.CheckBinary())
}

// statusServer answers /status with a height that rises by one per request.
func statusServer(t *testing.T, startAt int64) *httptest.Server {
	t.Helper()
	var height atomic.Int64
	height.Store(startAt)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		h := height.Add(1)
		_, _ = w.Write([]byte(`{"result":{"sync_info":{"latest_block_height":"` + strconv.FormatInt(h, 10) + `","catching_up":false}}}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func fakeNode(t *testing.T, srv *httptest.Server) *Node {
	t.Helper()
	return &Node{done: make(chan error, 1), RPC: strings.TrimPrefix(srv.URL, "http://"), Home: t.TempDir(), Start: time.Now()}
}

func TestNodeWaitHeight_reachesTheHeight(t *testing.T) {
	n := fakeNode(t, statusServer(t, 0))
	elapsed, err := n.WaitHeight(context.Background(), 3, 10*time.Second)
	require.NoError(t, err)
	require.Positive(t, elapsed)
	rpc, err := fakeNode(t, statusServer(t, 0)).WaitRPC(context.Background(), 10*time.Second)
	require.NoError(t, err)
	require.Positive(t, rpc)
}

func TestNodeWaitHeight_failsWhenTheNodeExits(t *testing.T) {
	n := fakeNode(t, statusServer(t, -1_000_000))
	require.NoError(t, os.WriteFile(filepath.Join(n.Home, logName), []byte("panic: store is corrupt"), 0o600))
	n.done <- context.Canceled
	_, err := n.WaitHeight(context.Background(), 5, 10*time.Second)
	require.ErrorContains(t, err, "exited before height 5")
	require.ErrorContains(t, err, "store is corrupt", "the node's log tail explains why")
}

func TestNodeWaitHeight_failsAfterTheBudget(t *testing.T) {
	n := fakeNode(t, statusServer(t, -1_000_000))
	_, err := n.WaitHeight(context.Background(), 5, 200*time.Millisecond)
	require.ErrorContains(t, err, "not reached within")
}

func TestPrune_removesBlocksBelowTheRetainHeight(t *testing.T) {
	res, err := Prune(context.Background(), PruneConfig{WorkDir: t.TempDir(), Blocks: 120, TxsPerBlock: 3, TxBytes: 100, Window: 40})
	require.NoError(t, err)
	require.Equal(t, int64(80), res.RetainHeight, "the tip less the window")
	require.Equal(t, uint64(79), res.Pruned, "blocks 1 to 79 go")
	require.Positive(t, res.StoreBytesBefore)
	require.Positive(t, res.MillisPer1000)
}

func TestPrune_neverPastTheLastArchivedHeight(t *testing.T) {
	res, err := Prune(context.Background(), PruneConfig{WorkDir: t.TempDir(), Blocks: 120, TxsPerBlock: 1, TxBytes: 50, Window: 40, LastArchived: 30})
	require.NoError(t, err)
	require.Equal(t, int64(30), res.RetainHeight, "a stalled archive holds the retain height back")
	require.Equal(t, uint64(29), res.Pruned)
}

func TestPrune_refusesWhenNothingMayGo(t *testing.T) {
	_, err := Prune(context.Background(), PruneConfig{WorkDir: t.TempDir(), Blocks: 30, TxsPerBlock: 1, TxBytes: 50, Window: 40})
	require.ErrorContains(t, err, "nothing be pruned")
	_, err = Prune(context.Background(), PruneConfig{WorkDir: t.TempDir(), Blocks: 100, TxsPerBlock: 1, TxBytes: 50, Window: 40, LastArchived: -1})
	require.ErrorContains(t, err, "nothing be pruned", "no archived block, no pruning")
}

func TestPrune_stopsWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Prune(ctx, PruneConfig{WorkDir: t.TempDir(), Blocks: 100, TxsPerBlock: 1, TxBytes: 50, Window: 40})
	require.ErrorIs(t, err, context.Canceled)
}
