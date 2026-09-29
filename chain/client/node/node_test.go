package node

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	abci "github.com/cometbft/cometbft/abci/types"
	coretypes "github.com/cometbft/cometbft/rpc/core/types"
	rpctypes "github.com/cometbft/cometbft/rpc/jsonrpc/types"

	"cosmossdk.io/math"

	gogoproto "github.com/cosmos/gogoproto/proto"

	"github.com/DeBrosOfficial/network/chain/x/storage/types"
)

// fakeRPC answers CometBFT JSON-RPC calls from a table of method results.
func fakeRPC(t *testing.T, results map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req rpctypes.RPCRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		res, ok := results[req.Method]
		var resp rpctypes.RPCResponse
		if ok {
			resp = rpctypes.NewRPCSuccessResponse(req.ID, res)
		} else {
			resp = rpctypes.RPCMethodNotFoundError(req.ID)
		}
		require.NoError(t, json.NewEncoder(w).Encode(resp))
	}))
}

func abciAnswer(t *testing.T, code uint32, log string, msg gogoproto.Message) *coretypes.ResultABCIQuery {
	t.Helper()
	var value []byte
	if msg != nil {
		var err error
		value, err = gogoproto.Marshal(msg)
		require.NoError(t, err)
	}
	space := ""
	if code != 0 {
		space = "sdk"
	}
	return &coretypes.ResultABCIQuery{Response: abci.ResponseQuery{Codespace: space, Code: code, Log: log, Value: value}}
}

func TestQuery_decodesAnAnswerAndReportsAFailure(t *testing.T) {
	want := types.QuerySlotResponse{Slot: types.Slot{DealId: 4, Index: 1, NodeId: "n2"}}
	srv := fakeRPC(t, map[string]any{"abci_query": abciAnswer(t, 0, "", &want)})
	defer srv.Close()
	c, err := Dial(srv.URL)
	require.NoError(t, err)

	var got types.QuerySlotResponse
	require.NoError(t, c.Query(context.Background(), "/orama.storage.v1.Query/Slot", &types.QuerySlotRequest{DealId: 4, Slot: 1}, &got))
	require.Equal(t, "n2", got.Slot.NodeId)

	bad := fakeRPC(t, map[string]any{"abci_query": abciAnswer(t, 22, "rpc error: code = NotFound desc = key not found", nil)})
	defer bad.Close()
	c2, err := Dial(bad.URL)
	require.NoError(t, err)
	err = c2.Query(context.Background(), "/orama.storage.v1.Query/Slot", &types.QuerySlotRequest{DealId: 9}, &got)
	var qe *QueryError
	require.ErrorAs(t, err, &qe)
	require.True(t, qe.NotFound())
	require.Equal(t, uint32(22), qe.Code)
}

func TestDial_refusesAnEmptyAddress(t *testing.T) {
	_, err := Dial("  ")
	require.Error(t, err)
}

func TestBlockEvents_dropsFailedTransactions(t *testing.T) {
	ok := abci.Event{Type: "ok"}
	failed := abci.Event{Type: "failed"}
	fin := abci.Event{Type: "finalize"}
	res := &coretypes.ResultBlockResults{
		FinalizeBlockEvents: []abci.Event{fin},
		TxsResults: []*abci.ExecTxResult{
			{Code: 0, Events: []abci.Event{ok}},
			{Code: 5, Events: []abci.Event{failed}},
			nil,
		},
	}
	got := blockEvents(res)
	require.Equal(t, []abci.Event{fin, ok}, got)
	require.Empty(t, blockEvents(&coretypes.ResultBlockResults{}))
}

func TestFeeFor_isGasTimesBaseFeeAndNeverZero(t *testing.T) {
	require.Equal(t, math.NewInt(300), feeFor(100, math.NewInt(3)))
	require.Equal(t, math.OneInt(), feeFor(100, math.ZeroInt()))
	require.Equal(t, math.OneInt(), feeFor(0, math.NewInt(3)))
}

func TestNewEncoding_signsStorageAndArchiveMessages(t *testing.T) {
	enc, err := newEncoding()
	require.NoError(t, err)
	for _, url := range []string{
		"/orama.storage.v1.MsgAcceptDeal",
		"/orama.storage.v1.MsgSubmitProofs",
		"/orama.archive.v1.MsgAttest",
	} {
		_, err := enc.registry.Resolve(url)
		require.NoErrorf(t, err, url)
	}
}
