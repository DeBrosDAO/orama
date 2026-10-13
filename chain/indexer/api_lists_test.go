package indexer

import (
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cockroachdb/pebble"
	"github.com/stretchr/testify/require"

	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	cnfttypes "github.com/DeBrosOfficial/network/chain/x/cnft/types"
)

func hashOf(tx fakeTx) string { return hex.EncodeToString(tx.raw.Hash()) }

// listChain indexes, in four blocks: a transfer, a refused transfer, a delegation and a cNFT tree.
func listChain(t *testing.T) (*Store, map[string]fakeTx, string, string) {
	t.Helper()
	alice, bob := addr(t, 1), addr(t, 2)
	val := newFakeValidator(t, 1, 10)
	txs := map[string]fakeTx{
		"send":   bankSend(t, alice, bob),
		"failed": failedTx(t, one(&banktypes.MsgSend{FromAddress: alice, ToAddress: bob}), alice, bob),
		"stake": okTx(t, one(&stakingtypes.MsgDelegate{DelegatorAddress: alice, ValidatorAddress: val.operator}),
			one(&stakingtypes.MsgDelegateResponse{}), alice),
		"tree": okTx(t, one(&cnfttypes.MsgCreateTree{Creator: bob, CollectionId: 3}), one(&cnfttypes.MsgCreateTreeResponse{Id: 1}), bob),
	}
	chain := newFakeChain()
	chain.add(txs["send"])
	chain.add(txs["failed"])
	chain.add(txs["stake"])
	chain.add(txs["tree"])
	return index(t, chain, 1), txs, alice, bob
}

func hashes(txs []Tx) []string {
	out := make([]string, len(txs))
	for i, t := range txs {
		out[i] = t.Hash
	}
	return out
}

func TestTxLists_aFilterListsWhatItNamesNewestFirst(t *testing.T) {
	store, txs, _, _ := listChain(t)

	for filter, want := range map[string][]string{
		FilterAll:       {hashOf(txs["tree"]), hashOf(txs["stake"]), hashOf(txs["failed"]), hashOf(txs["send"])},
		FilterTransfers: {hashOf(txs["failed"]), hashOf(txs["send"])},
		FilterFailed:    {hashOf(txs["failed"])},
		FilterStaking:   {hashOf(txs["stake"])},
		FilterStorage:   {},
	} {
		got, err := store.TxsFiltered(filter, 1, 10)
		require.NoError(t, err, filter)
		require.Equal(t, want, hashes(got), filter)
	}
}

func TestTxLists_pagesWalkAFilterWithoutRepeatsOrGaps(t *testing.T) {
	store, txs, _, _ := listChain(t)

	first, err := store.TxsFiltered(FilterTransfers, 1, 1)
	require.NoError(t, err)
	second, err := store.TxsFiltered(FilterTransfers, 2, 1)
	require.NoError(t, err)
	third, err := store.TxsFiltered(FilterTransfers, 3, 1)
	require.NoError(t, err)
	require.Equal(t, []string{hashOf(txs["failed"])}, hashes(first))
	require.Equal(t, []string{hashOf(txs["send"])}, hashes(second))
	require.Empty(t, third, "past the end is an empty page")
}

func TestTxLists_anAccountsListsAreItsOwn(t *testing.T) {
	store, txs, alice, bob := listChain(t)

	got, err := store.AccountTxsFiltered(alice, FilterStaking, 1, 10)
	require.NoError(t, err)
	require.Equal(t, []string{hashOf(txs["stake"])}, hashes(got))
	got, err = store.AccountTxsFiltered(bob, FilterStaking, 1, 10)
	require.NoError(t, err)
	require.Empty(t, got, "bob did not stake")
	got, err = store.AccountTxsFiltered(bob, FilterFailed, 1, 10)
	require.NoError(t, err)
	require.Equal(t, []string{hashOf(txs["failed"])}, hashes(got), "the refused transfer names bob")
	all, err := store.AccountTxsFiltered(alice, FilterAll, 1, 10)
	require.NoError(t, err)
	require.Len(t, all, 4-1, "alice is named by every transaction but the tree")
}

func TestTxListsAPI_takeAFilterAndAPage(t *testing.T) {
	store, txs, alice, _ := listChain(t)
	api := NewAPI(store, fixedTip{earliest: 1, latest: 4})

	code, body := get(t, api, http.MethodGet, "/index/v1/txs?filter=transfers&limit=1&page=2")
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, "transfers", body["filter"])
	require.Equal(t, 2.0, body["page"])
	list := body["txs"].([]any)
	require.Len(t, list, 1)
	require.Equal(t, hashOf(txs["send"]), list[0].(map[string]any)["hash"])

	code, body = get(t, api, http.MethodGet, "/index/v1/accounts/"+alice+"/txs?filter=staking")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, body["txs"], 1)

	code, body = get(t, api, http.MethodGet, "/index/v1/txs?filter=storage")
	require.Equal(t, http.StatusOK, code)
	require.Empty(t, body["txs"], "a filter nothing matches is an empty list, not an error")

	for _, target := range []string{
		"/index/v1/txs?filter=other", "/index/v1/txs?page=0", "/index/v1/txs?limit=x", "/index/v1/txs?kind=failed",
		"/index/v1/accounts/" + alice + "/txs?filter=bogus",
	} {
		code, _ = get(t, api, http.MethodGet, target)
		require.Equal(t, http.StatusBadRequest, code, target)
	}
}

func TestBlocksAPI_listNewestFirstWithTheCommitTallyAndNoHashes(t *testing.T) {
	chain, _ := econChain(t)
	for h := 1; h <= 5; h++ {
		chain.addBlock(votes(chain.valset, true, h%2 == 0, true), nil)
	}
	store, _ := follow(t, chain, t.TempDir(), 1)
	defer store.Close()
	api := NewAPI(store, fixedTip{earliest: 1, latest: 5})

	code, body := get(t, api, http.MethodGet, "/index/v1/blocks?limit=2")
	require.Equal(t, http.StatusOK, code)
	blocks := body["blocks"].([]any)
	require.Len(t, blocks, 2)
	newest := blocks[0].(map[string]any)
	require.Equal(t, 5.0, newest["height"])
	require.Equal(t, 2.0, newest["commit_signed"], "one of three did not vote in an odd block")
	require.Equal(t, 3.0, newest["commit_size"])
	require.NotContains(t, newest, "tx_hashes")
	require.Equal(t, 4.0, blocks[1].(map[string]any)["height"])

	_, body = get(t, api, http.MethodGet, "/index/v1/blocks?limit=2&page=3")
	require.Len(t, body["blocks"], 1, "the last page holds the one block left")
	require.Equal(t, 1.0, body["blocks"].([]any)[0].(map[string]any)["height"])
	require.Equal(t, 0.0, body["blocks"].([]any)[0].(map[string]any)["commit_size"], "the first block carries no votes")

	_, body = get(t, api, http.MethodGet, "/index/v1/blocks?page=9")
	require.Empty(t, body["blocks"])
}

func TestBlocksAPI_anEmptyIndexListsNoBlocks(t *testing.T) {
	store := openStore(t, t.TempDir())
	defer store.Close()
	code, body := get(t, NewAPI(store, fixedTip{earliest: 1, latest: 1}), http.MethodGet, "/index/v1/blocks")
	require.Equal(t, http.StatusOK, code)
	require.Empty(t, body["blocks"])
	require.NotNil(t, body["blocks"], "a list, not null")
}

func TestCacheControl_headDependentAnswersAreShortAndSettledOnesLonger(t *testing.T) {
	api, chain, alice, _ := apiFixture(t)
	hash := hex.EncodeToString(chain.blocks[1].txs[0].Hash())

	cache := func(target string) (int, string) {
		rec := httptest.NewRecorder()
		api.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		return rec.Code, rec.Header().Get("Cache-Control")
	}
	for target, want := range map[string]string{
		"/index/v1/status":                     "public, max-age=2",
		"/index/v1/operator/status":            "no-store",
		"/index/v1/txs":                        "public, max-age=2",
		"/index/v1/blocks":                     "public, max-age=2",
		"/index/v1/accounts/" + alice + "/txs": "public, max-age=2",
		"/index/v1/epochs":                     "public, max-age=2",
		"/index/v1/blocks/2":                   "public, max-age=600",
		"/index/v1/txs/" + hash:                "public, max-age=600",
		"/index/v1/stats/daily":                "public, max-age=2",
	} {
		code, got := cache(target)
		require.Equal(t, http.StatusOK, code, target)
		require.Equal(t, want, got, target)
	}
	for _, target := range []string{"/index/v1/blocks/99", "/index/v1/txs/" + hex.EncodeToString(make([]byte, 32)), "/index/v1/txs?page=0", "/index/v1/epochs/7", "/index/v1/nothing"} {
		code, got := cache(target)
		require.GreaterOrEqual(t, code, 400, target)
		require.Equal(t, "no-store", got, "an error is never cached: %s", target)
	}
}

func TestKinds_theTableNamesOnlyMessageKinds(t *testing.T) {
	require.NotEmpty(t, messageKinds)
	require.Equal(t, FilterTransfers, messageKinds["/cosmos.bank.v1beta1.MsgSend"])
	require.Equal(t, FilterStaking, messageKinds["/cosmos.staking.v1beta1.MsgDelegate"])
	require.Equal(t, FilterStorage, messageKinds["/orama.storage.v1.MsgCreateDeal"])
	require.Panics(t, func() { mustLoadKinds([]byte(`{"/x.MsgY":"failed"}`)) }, "failed is a status, not a kind")
	require.Panics(t, func() { mustLoadKinds([]byte(`{"/x.MsgY":"mystery"}`)) })
	require.Panics(t, func() { mustLoadKinds([]byte(`[1]`)) })
}

func TestKinds_aTransactionIsListedOncePerKind(t *testing.T) {
	require.Equal(t, []string{FilterTransfers}, kindsOf([]string{"/cosmos.bank.v1beta1.MsgSend", "/cosmos.bank.v1beta1.MsgSend"}, false))
	require.Equal(t, []string{FilterFailed, FilterTransfers, FilterStaking},
		kindsOf([]string{"/cosmos.bank.v1beta1.MsgSend", "/cosmos.staking.v1beta1.MsgDelegate"}, true))
	require.Empty(t, kindsOf([]string{"/orama.cnft.v1.MsgCreateTree"}, false))
	require.Empty(t, kindsOf(nil, false))
}

func TestBlocks_aHoleInTheIndexIsAnErrorNotAShortPage(t *testing.T) {
	chain, _ := econChain(t)
	for h := 1; h <= 5; h++ {
		chain.addBlock(votes(chain.valset, true, true, true), nil)
	}
	store, _ := follow(t, chain, t.TempDir(), 1)
	defer store.Close()
	require.NoError(t, store.db.Delete(blockKey(3), pebble.Sync))

	_, err := store.Blocks(1, 5)
	require.ErrorContains(t, err, "but not block 3")
	newest, err := store.Blocks(1, 2)
	require.NoError(t, err, "a page that does not reach the hole is fine")
	require.Len(t, newest, 2)
	_, err = store.Blocks(2, 2)
	require.ErrorContains(t, err, "but not block 3")
}

func TestBlocks_pagesCoverEveryBlockOnceAndStopAtTheStart(t *testing.T) {
	chain, _ := econChain(t)
	for h := 1; h <= 7; h++ {
		chain.addBlock(votes(chain.valset, true, true, true), nil)
	}
	store, _ := follow(t, chain, t.TempDir(), 3)
	defer store.Close()

	var heights []int64
	for page := 1; page <= 4; page++ {
		blocks, err := store.Blocks(page, 2)
		require.NoError(t, err)
		for _, b := range blocks {
			heights = append(heights, b.Height)
		}
	}
	require.Equal(t, []int64{7, 6, 5, 4, 3}, heights, "from the cursor down to the index's start height, each once")
}
