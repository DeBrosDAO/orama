package node

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	abci "github.com/cometbft/cometbft/abci/types"
	coretypes "github.com/cometbft/cometbft/rpc/core/types"
	rpctypes "github.com/cometbft/cometbft/rpc/jsonrpc/types"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	authsigning "github.com/cosmos/cosmos-sdk/x/auth/signing"

	gogoproto "github.com/cosmos/gogoproto/proto"

	nodestypes "github.com/DeBrosOfficial/network/chain/x/nodes/types"
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

func TestQueryAt_asksForTheHeightAndRefusesANegativeOne(t *testing.T) {
	var gotHeight string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req rpctypes.RPCRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		var params map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(req.Params, &params))
		gotHeight = string(params["height"])
		ans := abciAnswer(t, 0, "", &types.QuerySlotResponse{Slot: types.Slot{NodeId: "n1"}})
		require.NoError(t, json.NewEncoder(w).Encode(rpctypes.NewRPCSuccessResponse(req.ID, ans)))
	}))
	defer srv.Close()
	c, err := Dial(srv.URL)
	require.NoError(t, err)

	var got types.QuerySlotResponse
	require.NoError(t, c.QueryAt(context.Background(), 41, "/orama.storage.v1.Query/Slot", &types.QuerySlotRequest{DealId: 1}, &got))
	require.Equal(t, `"41"`, gotHeight)
	require.Equal(t, "n1", got.Slot.NodeId)

	require.NoError(t, c.Query(context.Background(), "/orama.storage.v1.Query/Slot", &types.QuerySlotRequest{DealId: 1}, &got))
	require.Equal(t, `"0"`, gotHeight, "Query reads the latest state")

	require.Error(t, c.QueryAt(context.Background(), -1, "/orama.storage.v1.Query/Slot", &types.QuerySlotRequest{}, &got))
}

func TestHeightRange_returnsEarliestAndLatest(t *testing.T) {
	st := &coretypes.ResultStatus{SyncInfo: coretypes.SyncInfo{EarliestBlockHeight: 120, LatestBlockHeight: 900}}
	srv := fakeRPC(t, map[string]any{"status": st})
	defer srv.Close()
	c, err := Dial(srv.URL)
	require.NoError(t, err)
	earliest, latest, err := c.HeightRange(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(120), earliest)
	require.Equal(t, int64(900), latest)
}

func TestBlockResults_keepsFailedTransactions(t *testing.T) {
	res := &coretypes.ResultBlockResults{
		Height: 7,
		TxsResults: []*abci.ExecTxResult{
			{Code: 0, GasUsed: 10},
			{Code: 5, Codespace: "sdk", GasUsed: 3},
		},
	}
	srv := fakeRPC(t, map[string]any{"block_results": res})
	defer srv.Close()
	c, err := Dial(srv.URL)
	require.NoError(t, err)
	got, err := c.BlockResults(context.Background(), 7)
	require.NoError(t, err)
	require.Len(t, got.TxsResults, 2)
	require.Equal(t, uint32(5), got.TxsResults[1].Code)

	missing := fakeRPC(t, map[string]any{})
	defer missing.Close()
	c2, err := Dial(missing.URL)
	require.NoError(t, err)
	_, err = c2.BlockResults(context.Background(), 7)
	require.ErrorContains(t, err, "block results at 7")
}

func TestDialWith_registersTheCallersInterfaces(t *testing.T) {
	plain, err := Dial("http://127.0.0.1:1")
	require.NoError(t, err)
	_, err = plain.registry.Resolve("/orama.nodes.v1.MsgRegisterOperator")
	require.Error(t, err, "the plain client registers no x/nodes message")

	c, err := DialWith("http://127.0.0.1:1", nodestypes.RegisterInterfaces)
	require.NoError(t, err)
	_, err = c.registry.Resolve("/orama.nodes.v1.MsgRegisterOperator")
	require.NoError(t, err)
}

func TestSubmitSignerless_broadcastsAnUnsignedTxWithExactlyTheGivenGas(t *testing.T) {
	var sent []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req rpctypes.RPCRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		var params map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(req.Params, &params))
		var result any
		switch req.Method {
		case "broadcast_tx_sync":
			require.NoError(t, json.Unmarshal(params["tx"], &sent))
			result = &coretypes.ResultBroadcastTx{Hash: []byte{1, 2, 3}}
		case "tx":
			result = &coretypes.ResultTx{Height: 9, TxResult: abci.ExecTxResult{Events: []abci.Event{{Type: "signerless"}}}}
		default:
			require.NoError(t, json.NewEncoder(w).Encode(rpctypes.RPCMethodNotFoundError(req.ID)))
			return
		}
		require.NoError(t, json.NewEncoder(w).Encode(rpctypes.NewRPCSuccessResponse(req.ID, result)))
	}))
	defer srv.Close()
	c, err := DialWith(srv.URL, nodestypes.RegisterInterfaces)
	require.NoError(t, err)
	c.includeTimeout = 5 * time.Second

	msg := &nodestypes.MsgRegisterOperator{Operator: "orama1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq"}
	_, events, err := c.SubmitSignerless(context.Background(), 20, msg)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.NotEmpty(t, sent, "the transaction reached broadcast_tx_sync")

	decoded, err := c.txConfig.TxDecoder()(sent)
	require.NoError(t, err)
	feeTx, ok := decoded.(sdk.FeeTx)
	require.True(t, ok)
	require.Equal(t, uint64(20), feeTx.GetGas(), "exactly the declared gas")
	require.True(t, feeTx.GetFee().IsZero(), "no fee")
	sigTx, ok := decoded.(authsigning.SigVerifiableTx)
	require.True(t, ok)
	sigs, err := sigTx.GetSignaturesV2()
	require.NoError(t, err)
	require.Empty(t, sigs, "no signature")
	require.Len(t, decoded.GetMsgs(), 1)
}

func TestSubmitSignerless_reportsACheckTxRefusal(t *testing.T) {
	srv := fakeRPC(t, map[string]any{"broadcast_tx_sync": &coretypes.ResultBroadcastTx{Code: 7, Log: "not a signer-less message", Hash: []byte{9}}})
	defer srv.Close()
	c, err := DialWith(srv.URL, nodestypes.RegisterInterfaces)
	require.NoError(t, err)
	_, _, err = c.SubmitSignerless(context.Background(), 20, &nodestypes.MsgRegisterOperator{Operator: "orama1x"})
	require.ErrorContains(t, err, "rejected by CheckTx (code 7)")
}
