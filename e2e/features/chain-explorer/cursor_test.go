//go:build e2e_fleet

package chainexplorer

import (
	"bytes"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
)

// maxListPage is the deepest numbered page of the lists that grow with the chain
// (chain/indexer/cursor.go MaxListPage, core/pkg/gateway/handlers/chainread indexMaxListPage).
const maxListPage = 10

type blockPage struct {
	Blocks []struct {
		Height int64 `json:"height"`
	} `json:"blocks"`
	NextCursor string `json:"next_cursor"`
}

// TestExplorerLists_aDeepPageIsReadByCursor: the block list's full page carries next_cursor, and
// the same route with cursor=<next_cursor> answers the blocks older than the page, with no page
// field. A page past maxListPage, a page with a cursor and a cursor that is not one are refused
// (website/src/docs/blockchain/reading-the-chain.mdx#the-chain-indexer).
func TestExplorerLists_aDeepPageIsReadByCursor(t *testing.T) {
	t.Parallel()
	requireIndexer(t)

	var first blockPage
	decode(t, read(t, "/index/blocks", url.Values{"limit": {"2"}}), &first)
	if len(first.Blocks) != 2 || first.NextCursor == "" {
		t.Fatalf("a full first page of 2 blocks with a next_cursor, got %+v", first)
	}
	var next blockPage
	decode(t, read(t, "/index/blocks", url.Values{"limit": {"2"}, "cursor": {first.NextCursor}}), &next)
	if len(next.Blocks) == 0 || next.Blocks[0].Height >= first.Blocks[1].Height {
		t.Fatalf("the cursor page must hold blocks older than %d, got %+v", first.Blocks[1].Height, next)
	}

	for _, q := range []url.Values{
		{"page": {strconv.Itoa(maxListPage + 1)}},
		{"page": {"2"}, "cursor": {first.NextCursor}},
		{"cursor": {"not-a-cursor"}},
	} {
		if resp := read(t, "/index/txs", q); resp.Status != http.StatusBadRequest {
			t.Errorf("txs %v: HTTP %d, want 400", q, resp.Status)
		}
	}
	if resp := read(t, "/index/txs", url.Values{"page": {strconv.Itoa(maxListPage)}}); resp.Status != http.StatusOK {
		t.Errorf("txs page %d: HTTP %d, want 200", maxListPage, resp.Status)
	}
}

type rewardPage struct {
	Rewards []struct {
		Epoch     uint64 `json:"epoch"`
		Validator string `json:"validator"`
	} `json:"rewards"`
	NextCursor string `json:"next_cursor"`
}

// TestExplorerLists_anAccountsRewardsAndAnOwnersAssetsPageByCursor: an account's rewards, newest
// epoch first, carry next_cursor on a full page and the same route with cursor=<next_cursor> answers
// the rewards after that page, with no page field. That list and an owner's assets both stop numbered
// pages at maxListPage, and refuse a page with a cursor, a cursor of another list's shape and a
// cursor that is not one (website/src/docs/blockchain/reading-the-chain.mdx#the-chain-indexer).
func TestExplorerLists_anAccountsRewardsAndAnOwnersAssetsPageByCursor(t *testing.T) {
	t.Parallel()
	requireIndexer(t)
	c := chain.New(t)
	account := c.Validator(t, c.Node(t, 0)).Address
	rewards, assets := "/index/accounts/"+account+"/rewards", "/index/cnft/owners/"+account+"/assets"

	var first rewardPage
	decode(t, read(t, rewards, url.Values{"limit": {"1"}}), &first)
	switch len(first.Rewards) {
	case 0:
		t.Logf("no reward is recorded for %s yet: only the refusals are checked", account)
	case 1:
		if first.NextCursor == "" {
			t.Fatalf("a full first page of 1 reward must carry a next_cursor, got %+v", first)
		}
		var next rewardPage
		resp := read(t, rewards, url.Values{"limit": {"1"}, "cursor": {first.NextCursor}})
		decode(t, resp, &next)
		if bytes.Contains(resp.Body, []byte(`"page"`)) {
			t.Errorf("a cursor page must carry no page field: %.200s", resp.Body)
		}
		if len(next.Rewards) == 1 {
			a, b := first.Rewards[0], next.Rewards[0]
			if b.Epoch > a.Epoch || (b.Epoch == a.Epoch && b.Validator >= a.Validator) {
				t.Errorf("the cursor page must hold rewards after epoch %d from %s, got %+v", a.Epoch, a.Validator, next)
			}
		}
	default:
		t.Fatalf("asked for 1 reward, got %d", len(first.Rewards))
	}

	assetCursor, rewardCursor := strings.Repeat("A", 59), first.NextCursor
	if rewardCursor == "" {
		rewardCursor = strings.Repeat("A", 79)
	}
	for path, own := range map[string]string{rewards: rewardCursor, assets: assetCursor} {
		if resp := read(t, path, url.Values{"page": {strconv.Itoa(maxListPage)}}); resp.Status != http.StatusOK {
			t.Errorf("%s page %d: HTTP %d, want 200", path, maxListPage, resp.Status)
		}
		for name, q := range map[string]url.Values{
			"a page past the bound": {"page": {strconv.Itoa(maxListPage + 1)}},
			"the old deepest page":  {"page": {"1000"}, "limit": {"100"}},
			"page with cursor":      {"page": {"2"}, "cursor": {own}},
			"a transaction cursor":  {"cursor": {"AAAAAAAAAAoAAAAB"}},
			"not a cursor":          {"cursor": {"not-a-cursor"}},
		} {
			if resp := read(t, path, q); resp.Status != http.StatusBadRequest {
				t.Errorf("%s %s %v: HTTP %d, want 400", path, name, q, resp.Status)
			}
		}
	}
	if resp := read(t, assets, url.Values{"cursor": {rewardCursor}}); resp.Status != http.StatusBadRequest {
		t.Errorf("an owner's assets given a rewards cursor: HTTP %d, want 400", resp.Status)
	}
}
