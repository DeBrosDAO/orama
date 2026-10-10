package indexer

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	cnfttypes "github.com/DeBrosOfficial/network/chain/x/cnft/types"
)

type fixedTip struct {
	earliest, latest int64
	err              error
}

func (f fixedTip) HeightRange(context.Context) (int64, int64, error) {
	return f.earliest, f.latest, f.err
}

// apiFixture indexes three bank sends from alice and one minted asset.
func apiFixture(t *testing.T) (*API, *fakeChain, string, []byte) {
	t.Helper()
	alice, bob := addr(t, 1), addr(t, 2)
	id := assetID(1)
	chain := newFakeChain()
	chain.add(
		okTx(t, one(&cnfttypes.MsgCreateTree{Creator: alice, CollectionId: 3}), one(&cnfttypes.MsgCreateTreeResponse{Id: 1}), alice),
		okTx(t, one(&cnfttypes.MsgMint{Creator: alice, TreeId: 1, Leaves: []cnfttypes.MintLeaf{{AssetId: id, Owner: alice, MetadataCid: "bafy"}}}),
			one(&cnfttypes.MsgMintResponse{LeafIndices: []uint32{0}}), alice),
	)
	chain.add(bankSend(t, alice, bob))
	chain.add(bankSend(t, alice, bob))
	store := index(t, chain, 1)
	return NewAPI(store, fixedTip{earliest: 1, latest: 9}), chain, alice, id
}

func get(t *testing.T, h http.Handler, method, target string) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), rec.Body.String())
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	return rec.Code, body
}

func TestAPI_servesTheIndex(t *testing.T) {
	api, chain, alice, id := apiFixture(t)

	code, body := get(t, api, http.MethodGet, "/index/v1/status")
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, map[string]any{"start_height": 1.0, "cursor": 3.0, "earliest": 1.0, "tip": 9.0}, body)

	code, body = get(t, api, http.MethodGet, "/index/v1/blocks/2")
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, 1.0, body["tx_count"])

	hash := hex.EncodeToString(chain.blocks[1].txs[0].Hash())
	code, body = get(t, api, http.MethodGet, "/index/v1/txs/"+strings.ToUpper(hash))
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, hash, body["hash"])
	require.Equal(t, []any{"/cosmos.bank.v1beta1.MsgSend"}, body["messages"])

	code, body = get(t, api, http.MethodGet, "/index/v1/accounts/"+alice+"/txs?limit=2")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, body["txs"], 2)
	require.Equal(t, 3.0, body["txs"].([]any)[0].(map[string]any)["height"])
	code, body = get(t, api, http.MethodGet, "/index/v1/accounts/"+alice+"/txs?page=2&limit=2")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, body["txs"], 2)
	code, body = get(t, api, http.MethodGet, "/index/v1/accounts/"+alice+"/txs?page=3&limit=2")
	require.Equal(t, http.StatusOK, code)
	require.Empty(t, body["txs"])

	code, body = get(t, api, http.MethodGet, "/index/v1/cnft/assets/"+hex.EncodeToString(id))
	require.Equal(t, http.StatusOK, code)
	rec := body["records"].([]any)[0].(map[string]any)
	require.Equal(t, false, rec["burnt"])
	require.Equal(t, alice, rec["ownership"].(map[string]any)["owner"])
	require.Equal(t, []any{map[string]any{"group_key": "collection", "group_value": "3"}}, rec["grouping"])
	require.Equal(t, true, rec["compression"].(map[string]any)["compressed"])

	code, body = get(t, api, http.MethodGet, "/index/v1/cnft/owners/"+alice+"/assets")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, body["assets"], 1)
	require.Equal(t, 1.0, body["page"])
	require.Equal(t, float64(DefaultLimit), body["limit"])
}

func TestAPI_refusesBadParameters(t *testing.T) {
	api, _, alice, _ := apiFixture(t)
	hash := strings.Repeat("ab", 32)
	bad := []string{
		"/index/v1/blocks/0",
		"/index/v1/blocks/-1",
		"/index/v1/blocks/abc",
		"/index/v1/blocks/01",
		"/index/v1/blocks/99999999999999999999",
		"/index/v1/blocks/2?x=1",
		"/index/v1/status?verbose=1",
		"/index/v1/txs/abc",
		"/index/v1/txs/0x" + hash,
		"/index/v1/txs/" + hash + "00",
		"/index/v1/accounts/" + strings.ToUpper(alice) + "/txs",
		"/index/v1/accounts/cosmos1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqnrql8a/txs",
		"/index/v1/accounts/" + alice + "/txs?limit=0",
		"/index/v1/accounts/" + alice + "/txs?limit=101",
		"/index/v1/accounts/" + alice + "/txs?page=1001",
		"/index/v1/accounts/" + alice + "/txs?page=0",
		"/index/v1/accounts/" + alice + "/txs?page=1&page=2",
		"/index/v1/accounts/" + alice + "/txs?offset=5",
		"/index/v1/accounts/" + alice + "/txs?limit=",
		"/index/v1/accounts/" + alice + "/txs?limit=1;page=2",
		"/index/v1/cnft/assets/xyz",
		"/index/v1/cnft/assets/" + hash + "?full=1",
		"/index/v1/cnft/owners/notanaddress/assets",
		"/index/v1/cnft/owners/" + alice + "/assets?limit=500",
	}
	for _, target := range bad {
		code, _ := get(t, api, http.MethodGet, target)
		require.Equal(t, http.StatusBadRequest, code, target)
	}
}

func TestAPI_refusesUnknownPathsAndMethods(t *testing.T) {
	api, _, alice, _ := apiFixture(t)
	for _, target := range []string{
		"/index/v1/",
		"/index/v1/blocks",
		"/index/v1/blocks/1/txs",
		"/index/v1//blocks/1",
		"/index/v1/blocks/1/",
		"/index/v1/./status",
		"/index/v1/%73tatus",
		"/index/v2/status",
		"/index/v1/accounts/" + alice + "/x",
		"/index/v1/cnft/owners/" + alice,
		"/status",
	} {
		code, _ := get(t, api, http.MethodGet, target)
		require.Equal(t, http.StatusNotFound, code, target)
	}
	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/index/v1/status", nil))
	require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	require.Equal(t, http.MethodGet, rec.Header().Get("Allow"))
}

func TestAPI_notIndexedIsNotFound(t *testing.T) {
	api, _, _, _ := apiFixture(t)
	for _, target := range []string{
		"/index/v1/blocks/4",
		"/index/v1/txs/" + strings.Repeat("ab", 32),
		"/index/v1/cnft/assets/" + strings.Repeat("cd", 32),
	} {
		code, body := get(t, api, http.MethodGet, target)
		require.Equal(t, http.StatusNotFound, code, target)
		require.Equal(t, "not indexed", body["error"])
	}
	code, body := get(t, api, http.MethodGet, "/index/v1/cnft/owners/"+addr(t, 9)+"/assets")
	require.Equal(t, http.StatusOK, code)
	require.Empty(t, body["assets"])
}

func TestAPI_statusReportsAnUnreachableChain(t *testing.T) {
	api, _, _, _ := apiFixture(t)
	api.chain = fixedTip{err: errors.New("connection refused")}
	code, body := get(t, api, http.MethodGet, "/index/v1/status")
	require.Equal(t, http.StatusBadGateway, code)
	require.Equal(t, "chain unreachable", body["error"])
}
