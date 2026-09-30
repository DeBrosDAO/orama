package globalcmd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
	srv := chainAPI(t, `{"tx_response":{"height":"9","code":5,"raw_log":"insufficient funds"}}`)
	err := broadcastAndWait(context.Background(), srv.URL, []byte{1}, "opened deal")
	if err == nil || !strings.Contains(err.Error(), testTxHash) || !strings.Contains(err.Error(), "insufficient funds") {
		t.Fatalf("err = %v, want the block's refusal naming the hash", err)
	}
}

func TestBroadcastAndWait_succeedsOnceInABlock(t *testing.T) {
	srv := chainAPI(t, `{"tx_response":{"height":"9","code":0}}`)
	if err := broadcastAndWait(context.Background(), srv.URL, []byte{1}, "opened deal"); err != nil {
		t.Fatal(err)
	}
}
