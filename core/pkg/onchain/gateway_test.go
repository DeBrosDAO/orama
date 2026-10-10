package onchain

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/chainread"
)

func gatewayChain(t *testing.T, h http.HandlerFunc) Gateway {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return Gateway{Reader: &chainread.Reader{Gateway: srv.URL}}
}

func TestGateway_sendsATransactionThroughTheGatewayRoutes(t *testing.T) {
	var calls []string
	hash := strings.Repeat("AB", 32)
	g := gatewayChain(t, func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch {
		case strings.HasPrefix(r.URL.Path, "/v1/chain/query/cosmos.auth.v1beta1.Query/AccountInfo"):
			_, _ = w.Write([]byte(`{"info":{"address":"` + testOperator + `","account_number":"42","sequence":"7"}}`))
		case strings.HasPrefix(r.URL.Path, "/v1/chain/query/orama.fees.v1.Query/BaseFee"):
			_, _ = w.Write([]byte(`{"base_fee":"10"}`))
		case r.URL.Path == "/v1/chain/simulate":
			_, _ = w.Write([]byte(`{"gas_wanted":"10000000","gas_used":"100000","fee":{"denom":"norama","amount":"1"},"base_fee":"10"}`))
		case r.URL.Path == "/v1/chain/broadcast":
			_, _ = w.Write([]byte(`{"code":0,"codespace":"","log":"","tx_hash":"` + hash + `"}`))
		case r.URL.Path == "/v1/chain/tx":
			if r.URL.Query().Get("hash") != hash {
				t.Errorf("hash = %q", r.URL.Query().Get("hash"))
			}
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":-1,"result":{"hash":"` + hash + `","height":"88","tx_result":{"code":0,"log":""}}}`))
		default:
			http.NotFound(w, r)
		}
	})
	c, err := New(g, newSigner(), testChainID)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := c.WithdrawEarnings(context.Background(), "5")
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Height != 88 || receipt.Hash != hash {
		t.Errorf("receipt = %+v", receipt)
	}
	if len(calls) != 5 {
		t.Errorf("calls = %v", calls)
	}
}

func TestGateway_aMissingAccountIsErrAccountNotFound(t *testing.T) {
	g := gatewayChain(t, func(w http.ResponseWriter, r *http.Request) { http.Error(w, "not found on chain", http.StatusNotFound) })
	c, err := New(g, newSigner(), testChainID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.WithdrawEarnings(context.Background(), "5"); !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("err = %v, want ErrAccountNotFound", err)
	}
}

func TestGateway_accountRefusesAKeyThatIsNotThirtyThreeBytes(t *testing.T) {
	g := gatewayChain(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"info":{"pub_key":{"@type":"/cosmos.crypto.secp256k1.PubKey","key":"Ak9OKtmcNNYLm6Yog8lDGoQY+GcyEpYfl6d7Y3f80FtiC"},"account_number":"1","sequence":"2"}}`))
	})
	acct, err := g.Account(context.Background(), testOperator)
	if err == nil {
		t.Fatalf("a truncated key was accepted: %+v", acct)
	}
}

func TestGateway_accountReadsNumberSequenceAndKey(t *testing.T) {
	g := gatewayChain(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"info":{"pub_key":{"key":"Ak9OKtmcNNYLm6Yog8lDGoQY+GcyEpYfl6d7Y3f80Fti"},"account_number":"1","sequence":"2"}}`))
	})
	acct, err := g.Account(context.Background(), testOperator)
	if err != nil {
		t.Fatal(err)
	}
	if acct.Number != 1 || acct.Sequence != 2 || len(acct.PubKey) != 33 {
		t.Fatalf("account = %+v", acct)
	}
}

func TestGateway_waitIncludedReportsABlockThatRefusedTheTransaction(t *testing.T) {
	hash := strings.Repeat("cd", 32)
	g := gatewayChain(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"result":{"height":"9","tx_result":{"code":5,"log":"insufficient earnings\u001b[31m"}}}`))
	})
	_, err := g.WaitIncluded(context.Background(), hash)
	if err == nil || !strings.Contains(err.Error(), "insufficient earnings") || strings.ContainsRune(err.Error(), 0x1b) {
		t.Fatalf("err = %v", err)
	}
}

func TestGateway_waitIncludedRefusesAHashThatIsNotAHash(t *testing.T) {
	g := gatewayChain(t, func(w http.ResponseWriter, r *http.Request) { t.Error("the gateway was asked") })
	for _, hash := range []string{"", "xyz", strings.Repeat("a", 10), strings.Repeat("ab", 32) + "&x=1"} {
		if _, err := g.WaitIncluded(context.Background(), hash); err == nil {
			t.Errorf("hash %q accepted", hash)
		}
	}
}

func TestGateway_aRefusedSimulationIsReportedWithTheChainsReason(t *testing.T) {
	g := gatewayChain(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"code":5,"codespace":"sdk","log":"insufficient funds"}`))
	})
	_, err := g.SimulateGas(context.Background(), []byte{1})
	var refused *chainread.TxRefusedError
	if !errors.As(err, &refused) || refused.Log != "insufficient funds" {
		t.Fatalf("err = %v", err)
	}
}
