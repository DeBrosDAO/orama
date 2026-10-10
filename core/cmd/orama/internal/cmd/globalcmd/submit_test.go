package globalcmd

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/clusterreg"
)

var testTxHash = strings.Repeat("AB", 32)

// chainAPI admits every broadcast to the mempool and answers the lookup with result.
func chainAPI(t *testing.T, result string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/cosmos/tx/v1beta1/txs":
			_, _ = w.Write([]byte(`{"tx_response":{"code":0,"txhash":"` + testTxHash + `"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/cosmos/tx/v1beta1/txs/"+testTxHash:
			_, _ = w.Write([]byte(result))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// The storage smoke on stagenet printed "opened deal" for a transaction that was only in the
// mempool. Admission is not success: the block's verdict is.
func TestBroadcastAndWait_aTransactionItsBlockRefusesIsAFailure(t *testing.T) {
	srv := chainAPI(t, `{"tx_response":{"txhash":"`+testTxHash+`","height":"9","code":5,"raw_log":"insufficient funds"}}`)
	err := broadcastAndWait(context.Background(), srv.URL, []byte{1}, "opened deal")
	if err == nil || !strings.Contains(err.Error(), testTxHash) || !strings.Contains(err.Error(), "insufficient funds") {
		t.Fatalf("err = %v, want the block's refusal naming the hash", err)
	}
}

func TestBroadcastAndWait_succeedsOnceInABlock(t *testing.T) {
	srv := chainAPI(t, `{"tx_response":{"txhash":"`+testTxHash+`","height":"9","code":0}}`)
	if err := broadcastAndWait(context.Background(), srv.URL, []byte{1}, "opened deal"); err != nil {
		t.Fatal(err)
	}
}

// Bug: a chain REST that did not answer exited 1, the code for "something went
// wrong"; a script cannot tell that from a refused transaction, and only the
// former is worth retrying.
func TestChainErr_unreachableIsUnavailable(t *testing.T) {
	_, transport := clusterreg.FetchAccount(context.Background(), "http://127.0.0.1:1", "orama1x")
	if transport == nil {
		t.Fatal("nothing listens on port 1, yet the fetch succeeded")
	}
	for name, c := range map[string]struct {
		err  error
		want int
	}{
		"connection refused": {transport, clierr.CodeUnavailable},
		"gateway error":      {&clusterreg.StatusError{Code: 502}, clierr.CodeUnavailable},
		"client error":       {&clusterreg.StatusError{Code: 400}, clierr.CodeFailure},
		"the chain said no":  {errors.New("the transaction failed in block 9 (code 5): insufficient funds"), clierr.CodeFailure},
	} {
		err := chainErr(c.err, "read the chain account")
		if got := clierr.CodeOf(err); got != c.want {
			t.Errorf("%s: exit code %d, want %d", name, got, c.want)
		}
		if !strings.HasPrefix(err.Error(), "read the chain account: ") {
			t.Errorf("%s: message %q lost its step", name, err)
		}
		if !errors.Is(err, c.err) {
			t.Errorf("%s: the cause is not unwrappable", name)
		}
	}
}

func TestBroadcastAndWait_unreachableChainIsUnavailable(t *testing.T) {
	err := broadcastAndWait(context.Background(), "http://127.0.0.1:1", []byte{1}, "opened deal")
	if got := clierr.CodeOf(err); got != clierr.CodeUnavailable {
		t.Fatalf("exit code %d (%v), want %d", got, err, clierr.CodeUnavailable)
	}
}
