package indexer

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	gogoproto "github.com/cosmos/gogoproto/proto"

	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
)

// longChain indexes blocks of two transfers each (alice to bob, the second also failing in every
// third block), and returns the chain so more blocks can be added.
func longChain(t *testing.T, blocks int) (*fakeChain, *Store, *Follower, string, string) {
	t.Helper()
	alice, bob := addr(t, 1), addr(t, 2)
	chain := newFakeChain()
	addTransferBlocks(t, chain, alice, bob, blocks)
	store := openStore(t, t.TempDir())
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	f, err := NewFollower(chain, store, 1)
	require.NoError(t, err)
	_, err = f.Step(context.Background())
	require.NoError(t, err)
	return chain, store, f, alice, bob
}

func addTransferBlocks(t *testing.T, chain *fakeChain, alice, bob string, n int) {
	t.Helper()
	for range n {
		chain.add(bankSend(t, alice, bob), failedTx(t, []gogoproto.Message{&banktypes.MsgSend{FromAddress: alice, ToAddress: bob}}, alice, bob))
	}
}

func TestCursor_roundTripsAndRefusesWhatStringNeverWrites(t *testing.T) {
	c := Cursor{Height: 1_234_567, Index: 41}
	got, err := ParseCursor(c.String())
	require.NoError(t, err)
	require.Equal(t, c, got)
	require.Len(t, c.String(), CursorLen)

	raw := func(height uint64, index uint32) string {
		b := make([]byte, 12)
		b[0], b[1], b[2], b[3] = byte(height>>56), byte(height>>48), byte(height>>40), byte(height>>32)
		b[4], b[5], b[6], b[7] = byte(height>>24), byte(height>>16), byte(height>>8), byte(height)
		b[8], b[9], b[10], b[11] = byte(index>>24), byte(index>>16), byte(index>>8), byte(index)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	for name, s := range map[string]string{
		"empty":             "",
		"short":             c.String()[:CursorLen-1],
		"long":              c.String() + "A",
		"padded":            c.String()[:CursorLen-2] + "==",
		"standard alphabet": strings.Repeat("+", CursorLen),
		"slash":             strings.Repeat("/", CursorLen),
		"space":             c.String()[:CursorLen-1] + " ",
		"height zero":       raw(0, 0),
		"height too large":  raw(1<<63, 0),
		"all ones":          raw(^uint64(0), ^uint32(0)),
	} {
		_, err := ParseCursor(s)
		require.Error(t, err, name)
	}
	_, err = ParseCursor(raw(1, ^uint32(0)))
	require.NoError(t, err, "the largest index and the lowest height are positions")
}

func collect(t *testing.T, next func(after *Cursor) []Tx) []string {
	t.Helper()
	var out []string
	var after *Cursor
	for range 100 {
		page := next(after)
		if len(page) == 0 {
			return out
		}
		out = append(out, hashes(page)...)
		last := page[len(page)-1]
		after = &Cursor{Height: last.Height, Index: last.Index}
	}
	t.Fatal("a list did not end")
	return nil
}

func TestCursor_pagesWalkEveryListOnceWithoutRepeatsOrGaps(t *testing.T) {
	_, store, _, alice, _ := longChain(t, 12)

	for _, filter := range []string{FilterAll, FilterTransfers, FilterFailed} {
		whole, err := store.TxsFiltered(filter, 1, MaxLimit)
		require.NoError(t, err)
		walked := collect(t, func(after *Cursor) []Tx {
			var txs []Tx
			if after == nil {
				txs, err = store.TxsFiltered(filter, 1, 5)
			} else {
				txs, err = store.TxsFilteredAfter(filter, *after, 5)
			}
			require.NoError(t, err)
			return txs
		})
		require.Equal(t, hashes(whole), walked, "txs %s", filter)

		wholeAcct, err := store.AccountTxsFiltered(alice, filter, 1, MaxLimit)
		require.NoError(t, err)
		walked = collect(t, func(after *Cursor) []Tx {
			var txs []Tx
			if after == nil {
				txs, err = store.AccountTxsFiltered(alice, filter, 1, 7)
			} else {
				txs, err = store.AccountTxsFilteredAfter(alice, filter, *after, 7)
			}
			require.NoError(t, err)
			return txs
		})
		require.Equal(t, hashes(wholeAcct), walked, "account txs %s", filter)
	}
}

// A page number names a place that moves: a row that arrives while a reader pages shifts every page
// after it. A cursor names a row, so the next page is what followed it whenever it is read.
func TestCursor_aRowArrivingBetweenPagesDoesNotShiftTheNextPage(t *testing.T) {
	chain, store, f, alice, bob := longChain(t, 6)

	first, err := store.TxsFiltered(FilterAll, 1, 4)
	require.NoError(t, err)
	last := first[len(first)-1]
	byCursor, err := store.TxsFilteredAfter(FilterAll, Cursor{Height: last.Height, Index: last.Index}, 4)
	require.NoError(t, err)

	addTransferBlocks(t, chain, alice, bob, 3)
	_, err = f.Step(context.Background())
	require.NoError(t, err)

	again, err := store.TxsFilteredAfter(FilterAll, Cursor{Height: last.Height, Index: last.Index}, 4)
	require.NoError(t, err)
	require.Equal(t, hashes(byCursor), hashes(again), "the page after a row is the same before and after newer rows")
	shifted, err := store.TxsFiltered(FilterAll, 2, 4)
	require.NoError(t, err)
	require.NotEqual(t, hashes(byCursor), hashes(shifted), "a page number moved under the reader")
}

func TestCursor_positionsOutsideTheListAreEmptyOrTheNewestPage(t *testing.T) {
	_, store, _, _, _ := longChain(t, 4)

	past, err := store.TxsFilteredAfter(FilterAll, Cursor{Height: 1, Index: 0}, 10)
	require.NoError(t, err)
	require.Empty(t, past, "nothing is older than the first row of block 1")
	newest, err := store.TxsFilteredAfter(FilterAll, Cursor{Height: 1 << 40, Index: 0}, 3)
	require.NoError(t, err)
	want, err := store.TxsFiltered(FilterAll, 1, 3)
	require.NoError(t, err)
	require.Equal(t, hashes(want), hashes(newest), "a position past the head reads from the head, and no further")
	other, err := store.AccountTxsFilteredAfter(addr(t, 9), FilterAll, Cursor{Height: 3, Index: 0}, 10)
	require.NoError(t, err)
	require.Empty(t, other, "an account with no rows has none after any position")
}

func TestCursor_blocksFollowTheHeightOfTheLastBlockOfThePage(t *testing.T) {
	_, store, _, _, _ := longChain(t, 9)

	var heights []int64
	var after *Cursor
	for range 20 {
		var page []BlockSummary
		var err error
		if after == nil {
			page, err = store.Blocks(1, 4)
		} else {
			page, err = store.BlocksAfter(*after, 4)
		}
		require.NoError(t, err)
		if len(page) == 0 {
			break
		}
		for _, b := range page {
			heights = append(heights, b.Height)
		}
		after = &Cursor{Height: page[len(page)-1].Height}
	}
	require.Equal(t, []int64{9, 8, 7, 6, 5, 4, 3, 2, 1}, heights)

	head, err := store.BlocksAfter(Cursor{Height: 1 << 40}, 2)
	require.NoError(t, err)
	require.Equal(t, int64(9), head[0].Height, "a height past the head starts at the head")
}

func TestListsAPI_aCursorPagesAndNamesTheNextPage(t *testing.T) {
	_, store, _, alice, _ := longChain(t, 6)
	api := NewAPI(store, fixedTip{earliest: 1, latest: 6})

	for _, route := range []string{"/index/v1/txs", "/index/v1/accounts/" + alice + "/txs"} {
		var seen []string
		cursor := ""
		for range 10 {
			target := route + "?limit=5"
			if cursor != "" {
				target += "&cursor=" + cursor
			}
			code, body := get(t, api, http.MethodGet, target)
			require.Equal(t, http.StatusOK, code, target)
			if cursor != "" {
				require.NotContains(t, body, "page", "a cursor page is not a numbered page")
			}
			for _, tx := range body["txs"].([]any) {
				seen = append(seen, tx.(map[string]any)["hash"].(string))
			}
			next, more := body["next_cursor"].(string)
			if !more {
				require.Less(t, len(body["txs"].([]any)), 5, "only a short page ends the list")
				break
			}
			require.Len(t, next, CursorLen)
			cursor = next
		}
		require.Len(t, seen, 12, route)
		require.Len(t, uniq(seen), 12, "no row twice: %s", route)
	}

	code, body := get(t, api, http.MethodGet, "/index/v1/blocks?limit=4")
	require.Equal(t, http.StatusOK, code)
	next := body["next_cursor"].(string)
	code, body = get(t, api, http.MethodGet, "/index/v1/blocks?limit=4&cursor="+next)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, 2.0, body["blocks"].([]any)[0].(map[string]any)["height"], "blocks 6 to 3 were page one")
	require.NotContains(t, body, "filter", "blocks have no filter")
}

func uniq(in []string) map[string]bool {
	out := map[string]bool{}
	for _, s := range in {
		out[s] = true
	}
	return out
}

func TestListsAPI_deepPageNumbersAndBadCursorsAreRefused(t *testing.T) {
	_, store, _, alice, _ := longChain(t, 3)
	api := NewAPI(store, fixedTip{earliest: 1, latest: 3})
	good := Cursor{Height: 3}.String()

	for _, route := range []string{"/index/v1/txs", "/index/v1/accounts/" + alice + "/txs", "/index/v1/blocks"} {
		code, _ := get(t, api, http.MethodGet, route+"?page=10")
		require.Equal(t, http.StatusOK, code, "the last numbered page: %s", route)
		for name, query := range map[string]string{
			"a page past the bound":    "page=11",
			"the old deepest page":     "page=1000&limit=100",
			"page with cursor":         "page=1&cursor=" + good,
			"cursor repeated":          "cursor=" + good + "&cursor=" + good,
			"cursor too short":         "cursor=" + good[:CursorLen-1],
			"cursor too long":          "cursor=" + good + good,
			"cursor of the wrong kind": "cursor=" + strings.Repeat("+", CursorLen),
			"height zero":              "cursor=" + Cursor{}.String(),
			"empty cursor":             "cursor=",
		} {
			code, body := get(t, api, http.MethodGet, route+"?"+query)
			require.Equal(t, http.StatusBadRequest, code, "%s %s", route, name)
			require.Contains(t, body, "error")
		}
	}
	code, _ := get(t, api, http.MethodGet, "/index/v1/blocks?cursor="+Cursor{Height: 3, Index: 1}.String())
	require.Equal(t, http.StatusBadRequest, code, "a block cursor has no index in the block")

	code, _ = get(t, api, http.MethodGet, "/index/v1/epochs?page=11")
	require.Equal(t, http.StatusOK, code, "the short aggregate lists keep their page bound")
	code, _ = get(t, api, http.MethodGet, "/index/v1/epochs?cursor="+good)
	require.Equal(t, http.StatusBadRequest, code, "only the long lists take a cursor")
}

// countingDB counts the reads of each key through a snapshot.
type countingDB struct {
	iterable
	reads map[string]int
}

func (c *countingDB) Get(key []byte) ([]byte, io.Closer, error) {
	c.reads[string(key)]++
	return c.iterable.Get(key)
}

func TestBlockPage_readsTheCursorOnce(t *testing.T) {
	_, store, _, _, _ := longChain(t, 5)
	snap := store.db.NewSnapshot()
	defer snap.Close()

	db := &countingDB{iterable: snap, reads: map[string]int{}}
	page, err := blockPage(db, 1, 3)
	require.NoError(t, err)
	require.Len(t, page, 3)
	require.Equal(t, 1, db.reads[keyCursor], "blockPage reads the index cursor once and hands it to blocksFrom")

	db = &countingDB{iterable: snap, reads: map[string]int{}}
	top, err := blocksFrom(db, 3, 5, 2)
	require.NoError(t, err)
	require.Len(t, top, 2)
	require.Zero(t, db.reads[keyCursor], "blocksFrom takes the cursor and does not read it")
}
