//go:build e2e_fleet

package chainexplorer

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/harness"
)

const (
	homeBlocks       = 24
	headMaxAge       = "public, max-age=2"
	settledMaxAge    = "public, max-age=600"
	noStore          = "no-store"
	headStreamPath   = "/v1/chain/head/stream"
	headStreamBudget = 20 * time.Second
)

func cacheControlOf(t *testing.T, path string, query url.Values) (int, string) {
	t.Helper()
	resp := read(t, path, query)
	return resp.Status, resp.Header.Get("Cache-Control")
}

// TestExplorerHome_oneReadHoldsThePage: GET /index/home answers everything the explorer's home page
// shows in one read: the index's position against the chain's tip, the newest blocks (newest first,
// each with its commit tally) and transactions, 48 hourly buckets, the epoch in progress and the
// chain's supply and base fee at its tip. It takes no query.
func TestExplorerHome_oneReadHoldsThePage(t *testing.T) {
	t.Parallel()
	requireIndexer(t)

	var home struct {
		Status struct {
			Cursor int64 `json:"cursor"`
			Tip    int64 `json:"tip"`
			Lag    int64 `json:"lag"`
		} `json:"status"`
		Blocks []struct {
			Height     int64 `json:"height"`
			CommitSize int   `json:"commit_size"`
		} `json:"blocks"`
		Txs   []json.RawMessage `json:"txs"`
		Hours []json.RawMessage `json:"hours"`
		Epoch struct {
			Number          uint64 `json:"number"`
			DurationSeconds int64  `json:"duration_seconds"`
		} `json:"epoch"`
		Supply       string `json:"supply"`
		BaseFee      string `json:"base_fee"`
		SupplyHeight int64  `json:"supply_height"`
	}
	resp := read(t, "/index/home", nil)
	decode(t, resp, &home)
	if home.Status.Cursor < 1 || home.Status.Tip < home.Status.Cursor || home.Status.Lag != home.Status.Tip-home.Status.Cursor {
		t.Errorf("home status %+v: want 1 <= cursor <= tip and lag = tip - cursor", home.Status)
	}
	if len(home.Blocks) == 0 || len(home.Blocks) > homeBlocks || home.Blocks[0].Height != home.Status.Cursor {
		t.Fatalf("%d blocks (newest %d), want 1 to %d, the newest at the cursor %d", len(home.Blocks), firstHeight(home.Blocks), homeBlocks, home.Status.Cursor)
	}
	for i := 1; i < len(home.Blocks); i++ {
		if home.Blocks[i].Height != home.Blocks[i-1].Height-1 {
			t.Errorf("block %d follows block %d in a newest-first list", home.Blocks[i].Height, home.Blocks[i-1].Height)
		}
	}
	if home.Blocks[0].CommitSize == 0 && home.Blocks[0].Height > 1 {
		t.Errorf("block %d carries no commit tally", home.Blocks[0].Height)
	}
	if len(home.Hours) != hourlyBuckets || home.Epoch.Number == 0 || home.Epoch.DurationSeconds <= 0 {
		t.Errorf("home holds %d hourly buckets and epoch %+v, want %d and a running epoch", len(home.Hours), home.Epoch, hourlyBuckets)
	}
	if _, ok := new(big.Int).SetString(home.Supply, 10); !ok || home.BaseFee == "" || home.SupplyHeight < home.Status.Cursor {
		t.Errorf("supply %q, base fee %q at height %d: want the chain's figures at its tip", home.Supply, home.BaseFee, home.SupplyHeight)
	}

	if got := resp.Header.Get("Cache-Control"); got != headMaxAge {
		t.Errorf("home Cache-Control %q, want %q: it depends on the head", got, headMaxAge)
	}
	if bad := read(t, "/index/home", url.Values{"limit": {"1"}}); bad.Status != http.StatusBadRequest {
		t.Errorf("home with a query: HTTP %d, want 400", bad.Status)
	}
}

func firstHeight(blocks []struct {
	Height     int64 `json:"height"`
	CommitSize int   `json:"commit_size"`
}) int64 {
	if len(blocks) == 0 {
		return 0
	}
	return blocks[0].Height
}

// TestExplorerLists_pagesAndFiltersAreTheIndexs: the block list and the transaction lists page on
// the server. Blocks are newest first with no transaction hashes, a page past the oldest is empty,
// and the transaction filters (failed, transfers, staking, storage) list only what they name, again
// newest first. A value outside the filters or the bounds is refused before the indexer is asked.
func TestExplorerLists_pagesAndFiltersAreTheIndexs(t *testing.T) {
	t.Parallel()
	requireIndexer(t)

	var blocks struct {
		Page   int `json:"page"`
		Blocks []struct {
			Height int64 `json:"height"`
		} `json:"blocks"`
	}
	decode(t, read(t, "/index/blocks", url.Values{"limit": {fmt.Sprint(indexListLimit)}}), &blocks)
	if len(blocks.Blocks) == 0 || len(blocks.Blocks) > indexListLimit {
		t.Fatalf("%d blocks, want 1 to %d", len(blocks.Blocks), indexListLimit)
	}
	for i := 1; i < len(blocks.Blocks); i++ {
		if blocks.Blocks[i].Height >= blocks.Blocks[i-1].Height {
			t.Errorf("blocks %d then %d are not newest first", blocks.Blocks[i-1].Height, blocks.Blocks[i].Height)
		}
	}
	if resp := read(t, "/index/blocks", url.Values{"limit": {fmt.Sprint(indexListLimit)}}); strings.Contains(string(resp.Body), "tx_hashes") {
		t.Errorf("the block list carries transaction hashes: %.200s", resp.Body)
	}
	var past struct {
		Blocks []any `json:"blocks"`
	}
	decode(t, read(t, "/index/blocks", url.Values{"page": {fmt.Sprint(maxIndexPage)}, "limit": {fmt.Sprint(maxIndexLimit)}}), &past)
	if len(past.Blocks) != 0 {
		t.Errorf("page %d of blocks holds %d rows", maxIndexPage, len(past.Blocks))
	}

	for _, filter := range []string{"all", "failed", "transfers", "staking", "storage"} {
		var list struct {
			Filter string `json:"filter"`
			Txs    []struct {
				Height   int64    `json:"height"`
				Index    uint32   `json:"index"`
				Code     uint32   `json:"code"`
				Messages []string `json:"messages"`
			} `json:"txs"`
		}
		decode(t, read(t, "/index/txs", url.Values{"filter": {filter}, "limit": {fmt.Sprint(maxIndexLimit)}}), &list)
		if list.Filter != filter || len(list.Txs) > maxIndexLimit {
			t.Errorf("filter %s: answered filter %q with %d rows", filter, list.Filter, len(list.Txs))
		}
		for i, tx := range list.Txs {
			if filter == "failed" && tx.Code == 0 {
				t.Errorf("the failed list holds a successful transaction at %d/%d", tx.Height, tx.Index)
			}
			if i > 0 && (tx.Height > list.Txs[i-1].Height || (tx.Height == list.Txs[i-1].Height && tx.Index >= list.Txs[i-1].Index)) {
				t.Errorf("filter %s: transaction %d/%d follows %d/%d in a newest-first list", filter, tx.Height, tx.Index, list.Txs[i-1].Height, list.Txs[i-1].Index)
			}
		}
	}

	c := chain.New(t)
	account := c.Validator(t, c.Node(t, 0)).Address
	for _, tc := range []struct {
		path  string
		query url.Values
	}{
		{"/index/txs", url.Values{"filter": {"nonsense"}}},
		{"/index/txs", url.Values{"filter": {"failed", "all"}}},
		{"/index/txs", url.Values{"page": {"0"}}},
		{"/index/txs", url.Values{"page": {fmt.Sprint(maxIndexPage + 1)}}},
		{"/index/blocks", url.Values{"filter": {"failed"}}},
		{"/index/blocks", url.Values{"limit": {fmt.Sprint(maxIndexLimit + 1)}}},
		{"/index/accounts/" + account + "/txs", url.Values{"filter": {"bogus"}}},
	} {
		if resp := read(t, tc.path, tc.query); resp.Status != http.StatusBadRequest {
			t.Errorf("GET %s %v: HTTP %d, want 400", tc.path, tc.query, resp.Status)
		}
	}
	var mine struct {
		Txs []json.RawMessage `json:"txs"`
	}
	decode(t, read(t, "/index/accounts/"+account+"/txs", url.Values{"filter": {"staking"}, "limit": {fmt.Sprint(indexListLimit)}}), &mine)
	if len(mine.Txs) == 0 {
		t.Errorf("%s bonded its validator and its staking list is empty", account)
	}
}

// TestExplorerCache_headAnswersAreShortAndSettledOnesLongerAndErrorsNeverCached: the indexer says
// how long an answer may be kept and the gateway passes it on: a few seconds for what depends on the
// head, ten minutes for a block, a transaction by hash or a closed epoch, and nothing for a refusal
// or a miss. A caller can therefore not make the gateway cache a form it does not write.
func TestExplorerCache_headAnswersAreShortAndSettledOnesLongerAndErrorsNeverCached(t *testing.T) {
	t.Parallel()
	requireIndexer(t)

	for path, want := range map[string]string{
		"/index/status":   headMaxAge,
		"/index/txs":      headMaxAge,
		"/index/blocks":   headMaxAge,
		"/index/epochs":   headMaxAge,
		"/index/blocks/1": settledMaxAge,
	} {
		code, got := cacheControlOf(t, path, nil)
		if code == http.StatusNotFound && path == "/index/blocks/1" {
			// An index that began after block 1 holds no block 1: use the oldest it has.
			var status struct {
				StartHeight int64 `json:"start_height"`
			}
			decode(t, read(t, "/index/status", nil), &status)
			code, got = cacheControlOf(t, fmt.Sprintf("/index/blocks/%d", status.StartHeight), nil)
		}
		if code != http.StatusOK || got != want {
			t.Errorf("GET %s: HTTP %d, Cache-Control %q, want 200 and %q", path, code, got, want)
		}
	}
	for _, tc := range []struct {
		path  string
		query url.Values
		want  int
	}{
		{"/index/blocks/999999999", nil, http.StatusNotFound},
		{"/index/txs", url.Values{"page": {"0"}}, http.StatusBadRequest},
		{fmt.Sprintf("/index/epochs/%d", unknownEpoch), nil, http.StatusNotFound},
	} {
		code, got := cacheControlOf(t, tc.path, tc.query)
		if code != tc.want || got != noStore {
			t.Errorf("GET %s %v: HTTP %d, Cache-Control %q, want %d and %q", tc.path, tc.query, code, got, tc.want, noStore)
		}
	}
	if got := read(t, "/status", nil).Header.Get("Cache-Control"); got != noStore {
		t.Errorf("the live status read is Cache-Control %q, want %q", got, noStore)
	}
}

// TestExplorerHead_streamAnnouncesTheHead: GET /v1/chain/head/stream is server-sent events. It opens
// with the chain's current head (a height and the run's chain id), needs no indexer, is not
// cached, and refuses another method and a query.
func TestExplorerHead_streamAnnouncesTheHead(t *testing.T) {
	t.Parallel()
	g := harness.GW(t)

	ctx, cancel := context.WithTimeout(context.Background(), headStreamBudget)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.BaseURL+headStreamPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := g.HTTP.Do(req)
	if err != nil {
		t.Fatalf("open the head stream: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") || resp.Header.Get("Cache-Control") != noStore {
		t.Fatalf("head stream: HTTP %d, Content-Type %q, Cache-Control %q", resp.StatusCode, resp.Header.Get("Content-Type"), resp.Header.Get("Cache-Control"))
	}
	var event, data string
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "event: ") {
			event = strings.TrimPrefix(line, "event: ")
		}
		if strings.HasPrefix(line, "data: ") {
			data = strings.TrimPrefix(line, "data: ")
		}
		if line == "" && event != "" {
			break
		}
	}
	if event != "head" {
		t.Fatalf("the stream opened with event %q (data %q), want the head: %v", event, data, sc.Err())
	}
	var head struct {
		Height  int64  `json:"height"`
		Time    string `json:"time"`
		ChainID string `json:"chain_id"`
	}
	if err := json.Unmarshal([]byte(data), &head); err != nil || head.Height < 1 || head.ChainID == "" {
		t.Errorf("head event %q is not a height and a chain id: %v", data, err)
	}
	if _, err := time.Parse(time.RFC3339, head.Time); err != nil {
		t.Errorf("head time %q: %v", head.Time, err)
	}
	cancel()

	if bad := read(t, "/head/stream", url.Values{"from": {"1"}}); bad.Status != http.StatusBadRequest {
		t.Errorf("head stream with a query: HTTP %d, want 400", bad.Status)
	}
}
