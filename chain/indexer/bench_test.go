package indexer

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// The load test builds an index the way the follower leaves one, without a chain to follow, and reads
// it through the HTTP API. It is a benchmark, so `go test` skips it:
//
//	go test -run '^$' -bench BenchmarkIndexAtScale -benchtime 300x ./indexer/
//
// ORAMA_INDEX_BENCH_TXS sets the number of transactions (default one million).
const (
	benchDefaultTxs   = 1_000_000
	benchTxsPerBlock  = 10
	benchAccounts     = 20_000
	benchValidators   = 100
	benchEpochs       = 500
	benchBlocksPerEp  = 200
	benchBatchBlocks  = 1000
	benchBlockSeconds = 2
	benchFailedEvery  = 50
	// benchDeepRows is how far from the newest row the deep reads start (page 1000, 100 a page).
	benchDeepRows = 99_900
)

// BenchmarkIndexAtScale reads a large index through the API: the routes the explorer calls for a
// page, each as the HTTP handler answers it (the JSON is encoded; the network is not in it).
func BenchmarkIndexAtScale(b *testing.B) {
	txs := benchTxs(b)
	idx := buildSynthetic(b, filepath.Join(b.TempDir(), "index"), txs)
	defer idx.store.Close()
	b.Logf("index of %d transactions in %d blocks, %d accounts, %d epochs of %d validators: built in %s, %.0f MB on disk",
		idx.txs, idx.blocks, benchAccounts, idx.epochs, benchValidators, idx.buildTime.Round(time.Second), float64(idx.onDiskSize)/(1<<20))
	api := NewAPI(idx.store, benchChain{tip: idx.blocks})
	deepest := fmt.Sprintf("page=%d&limit=%d", MaxListPage, MaxLimit)
	// deep and deepBlocks are the rows the old page 1000 at 100 a page read, 99,900 rows from the
	// newest, which a cursor reaches by seeking: ten transactions to a block, one block to a row.
	deep := fmt.Sprintf("cursor=%s&limit=%d", Cursor{Height: max(2, idx.blocks-int64(benchDeepRows/benchTxsPerBlock)+1)}.String(), MaxLimit)
	deepBlocks := fmt.Sprintf("cursor=%s&limit=%d", Cursor{Height: max(2, idx.blocks-int64(benchDeepRows)+1)}.String(), MaxLimit)
	lastEpoch := idx.epochs

	routes := []struct{ name, target string }{
		{"status", "/index/v1/status"},
		{"txs_latest_20", "/index/v1/txs?limit=20"},
		{"txs_page10_limit100", "/index/v1/txs?" + deepest},
		{"txs_deep_cursor_limit100", "/index/v1/txs?" + deep},
		{"txs_failed_page1", "/index/v1/txs?filter=failed&limit=20"},
		{"txs_transfers_deep_cursor", "/index/v1/txs?filter=transfers&" + deep},
		{"txs_storage_page1", "/index/v1/txs?filter=storage&limit=20"},
		{"account_txs_busy_page1", "/index/v1/accounts/" + idx.busy + "/txs?limit=20"},
		{"account_txs_busy_deep_cursor", "/index/v1/accounts/" + idx.busy + "/txs?" + deep},
		{"account_txs_busy_staking", "/index/v1/accounts/" + idx.busy + "/txs?filter=staking&limit=20"},
		{"account_summary", "/index/v1/accounts/" + idx.busy},
		{"tx_by_hash", "/index/v1/txs/" + idx.firstHash},
		{"blocks_page1_limit24", "/index/v1/blocks?limit=24"},
		{"blocks_page10_limit100", "/index/v1/blocks?" + deepest},
		{"blocks_deep_cursor_limit100", "/index/v1/blocks?" + deepBlocks},
		{"block_by_height", "/index/v1/blocks/" + strconv.FormatInt(idx.blocks/2, 10)},
		{"stats_hourly_48h", "/index/v1/stats"},
		{"stats_daily", "/index/v1/stats/daily?limit=30"},
		{"epochs_page1_limit100", "/index/v1/epochs?limit=100"},
		{"epoch_rewards_100_validators", "/index/v1/epochs/" + strconv.Itoa(lastEpoch) + "/rewards?limit=100"},
		{"validator_rewards_page1", "/index/v1/validators/" + idx.validator + "/rewards?limit=20"},
		{"account_rewards", "/index/v1/accounts/" + idx.busy + "/rewards"},
	}
	for _, rt := range routes {
		b.Run(rt.name, func(b *testing.B) { benchRoute(b, api, rt.target) })
	}
	b.Run("home_cached", func(b *testing.B) { benchRoute(b, api, "/index/v1/home") })
	b.Run("home_rebuilt", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			api.home = homeCache{}
			benchServe(b, api, "/index/v1/home")
		}
	})
}

func benchRoute(b *testing.B, api *API, target string) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchServe(b, api, target)
	}
}

func benchServe(b *testing.B, api *API, target string) {
	b.Helper()
	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	if rec.Code != http.StatusOK {
		b.Fatalf("%s answered %d: %s", target, rec.Code, rec.Body.String())
	}
	b.SetBytes(int64(rec.Body.Len()))
}
