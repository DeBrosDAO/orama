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
	var hash string
	g := gatewayChain(t, func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch {
		case r.URL.Path == "/v1/chain/status":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":-1,"result":{"sync_info":{"latest_block_height":"1000"}}}`))
		case strings.HasPrefix(r.URL.Path, "/v1/chain/query/cosmos.auth.v1beta1.Query/AccountInfo"):
			_, _ = w.Write([]byte(`{"info":{"address":"` + testOperator + `","account_number":"42","sequence":"7"}}`))
		case strings.HasPrefix(r.URL.Path, "/v1/chain/query/orama.fees.v1.Query/BaseFee"):
			_, _ = w.Write([]byte(`{"base_fee":"10"}`))
		case r.URL.Path == "/v1/chain/simulate":
			_, _ = w.Write([]byte(`{"gas_wanted":"10000000","gas_used":"100000","fee":{"denom":"norama","amount":"1"},"base_fee":"10"}`))
		case r.URL.Path == "/v1/chain/broadcast":
			hash = postedHash(t, r)
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
	if len(calls) != 6 {
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
		_, _ = w.Write([]byte(`{"result":{"hash":"` + hash + `","height":"9","tx_result":{"code":5,"log":"insufficient earnings\u001b[31m"}}}`))
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

func TestGateway_baseFeeMustBeAPlainInteger(t *testing.T) {
	for _, fee := range []string{"-1", "1e9", strings.Repeat("9", 30), "", "0x5", "1.5"} {
		g := gatewayChain(t, func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"base_fee":"` + fee + `"}`))
		})
		if got, err := g.BaseFee(context.Background()); err == nil {
			t.Errorf("base fee %q accepted as %q", fee, got)
		}
	}
	g := gatewayChain(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"base_fee":"25"}`)) })
	if got, err := g.BaseFee(context.Background()); err != nil || got != "25" {
		t.Fatalf("base fee %q, %v", got, err)
	}
}

// The gateway answers 422 for a refusal, but a 200 that carries a non-zero code is not an accepted
// transaction either.
func TestGateway_aNonZeroBroadcastCodeIsARefusal(t *testing.T) {
	g := gatewayChain(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":13,"codespace":"sdk","log":"insufficient fee\u001b[2J","tx_hash":"` + strings.Repeat("AB", 32) + `"}`))
	})
	_, err := g.Broadcast(context.Background(), []byte{1})
	var refused *chainread.TxRefusedError
	if !errors.As(err, &refused) || refused.Code != 13 {
		t.Fatalf("err = %v", err)
	}
	if strings.ContainsRune(err.Error(), 0x1b) {
		t.Fatalf("the refusal carries an escape sequence: %q", err.Error())
	}
}

func TestGateway_anAcceptedBroadcastWithoutAHashIsRefused(t *testing.T) {
	g := gatewayChain(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"code":0,"tx_hash":""}`)) })
	if _, err := g.Broadcast(context.Background(), []byte{1}); err == nil {
		t.Fatal("an answer with no hash was accepted")
	}
}

func TestGateway_whatTheGatewaySaysIsStrippedBeforeItIsPrinted(t *testing.T) {
	g := gatewayChain(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad \x1b[31mred\x1b[0m \u202eoverride", http.StatusBadGateway)
	})
	_, err := g.BaseFee(context.Background())
	if err == nil {
		t.Fatal("no error")
	}
	for _, bad := range []string{"\x1b", "\u202e"} {
		if strings.Contains(err.Error(), bad) {
			t.Errorf("error %q carries %q", err.Error(), bad)
		}
	}
}

func TestGateway_waitIncludedRefusesAResultForAnotherHash(t *testing.T) {
	hash := strings.Repeat("cd", 32)
	for name, body := range map[string]string{
		"another hash":               `{"result":{"hash":"` + strings.Repeat("EF", 32) + `","height":"9","tx_result":{"code":0}}}`,
		"no hash":                    `{"result":{"height":"9","tx_result":{"code":0}}}`,
		"another hash, failed there": `{"result":{"hash":"` + strings.Repeat("EF", 32) + `\u001b[2J","height":"9","tx_result":{"code":5,"log":"boom"}}}`,
	} {
		g := gatewayChain(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) })
		height, err := g.WaitIncluded(context.Background(), hash)
		if err == nil || !strings.Contains(err.Error(), "refusing to report it") || height != 0 || strings.ContainsRune(err.Error(), 0x1b) {
			t.Errorf("%s: height %d err %v", name, height, err)
		}
	}
}

func TestGateway_latestHeight(t *testing.T) {
	for body, want := range map[string]uint64{
		`{"result":{"sync_info":{"latest_block_height":"77"}}}`: 77,
		`{"sync_info":{"latest_block_height":"78"}}`:            78,
	} {
		g := gatewayChain(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) })
		if got, err := g.LatestHeight(context.Background()); err != nil || got != want {
			t.Errorf("%s: %d, %v", body, got, err)
		}
	}
	for _, body := range []string{`{}`, `{"result":{"sync_info":{"latest_block_height":"0"}}}`, `{"result":{"sync_info":{"latest_block_height":"-1"}}}`, `[1]`, `{"result":{"sync_info":{"latest_block_height":"1\u001b"}}}`} {
		g := gatewayChain(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) })
		if got, err := g.LatestHeight(context.Background()); err == nil {
			t.Errorf("%s accepted as %d", body, got)
		}
	}
}
