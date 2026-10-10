package onchain

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// REST drives the chain's real endpoints, in the order a transaction needs them.
func TestREST_sendsATransactionThroughTheNodesEndpoints(t *testing.T) {
	var calls []string
	var hash string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch r.URL.Path {
		case "/cosmos/auth/v1beta1/accounts/" + testOperator:
			_, _ = w.Write([]byte(`{"account":{"account_number":"42","sequence":"7"}}`))
		case "/cosmos/base/tendermint/v1beta1/blocks/latest":
			_, _ = w.Write([]byte(`{"block":{"header":{"height":"1000"}}}`))
		case "/orama/fees/v1/base-fee":
			_, _ = w.Write([]byte(`{"base_fee":"10"}`))
		case "/cosmos/tx/v1beta1/simulate":
			_, _ = w.Write([]byte(`{"gas_info":{"gas_used":"100000"}}`))
		case "/cosmos/tx/v1beta1/txs":
			hash = postedHash(t, r)
			_, _ = w.Write([]byte(`{"tx_response":{"code":0,"txhash":"` + hash + `"}}`))
		case "/cosmos/tx/v1beta1/txs/" + hash:
			_, _ = w.Write([]byte(`{"tx_response":{"txhash":"` + hash + `","height":"88","code":0}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c, err := New(REST{Base: srv.URL}, newSigner(), testChainID)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := c.RegisterOperator(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Height != 88 || receipt.Hash != hash {
		t.Errorf("receipt = %+v", receipt)
	}
	want := []string{
		"GET /cosmos/auth/v1beta1/accounts/" + testOperator,
		"GET /cosmos/base/tendermint/v1beta1/blocks/latest",
		"POST /cosmos/tx/v1beta1/simulate",
		"GET /orama/fees/v1/base-fee",
		"POST /cosmos/tx/v1beta1/txs",
		"GET /cosmos/tx/v1beta1/txs/" + hash,
	}
	if strings.Join(calls, "\n") != strings.Join(want, "\n") {
		t.Errorf("calls:\n%s\nwant:\n%s", strings.Join(calls, "\n"), strings.Join(want, "\n"))
	}
}

// postedHash is the hash of the transaction a broadcast request carries, as a node computes it.
func postedHash(t *testing.T, r *http.Request) string {
	t.Helper()
	var body struct {
		TxBytes string `json:"tx_bytes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(body.TxBytes)
	if err != nil {
		t.Fatal(err)
	}
	return TxHash(raw)
}

func TestREST_aNodeThatAnswersAnotherHashIsRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cosmos/auth/v1beta1/accounts/" + testOperator:
			_, _ = w.Write([]byte(`{"account":{"account_number":"42","sequence":"7"}}`))
		case "/cosmos/base/tendermint/v1beta1/blocks/latest":
			_, _ = w.Write([]byte(`{"block":{"header":{"height":"1000"}}}`))
		case "/orama/fees/v1/base-fee":
			_, _ = w.Write([]byte(`{"base_fee":"10"}`))
		case "/cosmos/tx/v1beta1/simulate":
			_, _ = w.Write([]byte(`{"gas_info":{"gas_used":"100000"}}`))
		case "/cosmos/tx/v1beta1/txs":
			_, _ = w.Write([]byte(`{"tx_response":{"code":0,"txhash":"` + strings.Repeat("CD", 32) + `"}}`))
		default:
			t.Errorf("the client followed the wrong hash: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c, _ := New(REST{Base: srv.URL}, newSigner(), testChainID)
	if _, err := c.RegisterOperator(context.Background()); err == nil || !strings.Contains(err.Error(), "refusing to follow or report") {
		t.Fatalf("err = %v", err)
	}
}

func TestREST_aMissingAccountIs404NotAGenericFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":5,"message":"account ` + testOperator + ` not found"}`))
	}))
	defer srv.Close()
	c, _ := New(REST{Base: srv.URL}, newSigner(), testChainID)
	_, err := c.RegisterOperator(context.Background())
	if err == nil || !strings.Contains(err.Error(), "does not exist on the chain yet") {
		t.Fatalf("error = %v", err)
	}
}
