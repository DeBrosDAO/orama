package clusterreg

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchAccountAndBroadcast(t *testing.T) {
	pub, _ := hex.DecodeString(vectorPubKey)
	var posted string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cosmos/auth/v1beta1/accounts/" + vectorAddress:
			w.Write([]byte(`{"account":{"account_number":"7","sequence":"3","pub_key":{"key":"` + base64.StdEncoding.EncodeToString(pub) + `"}}}`))
		case "/cosmos/tx/v1beta1/txs":
			posted = r.URL.Path
			w.Write([]byte(`{"tx_response":{"code":0,"txhash":"ABCDEF"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	acct, err := FetchAccount(context.Background(), srv.URL, vectorAddress)
	if err != nil {
		t.Fatal(err)
	}
	if acct.Number != 7 || acct.Sequence != 3 || hex.EncodeToString(acct.PubKey) != vectorPubKey {
		t.Fatalf("account %+v", acct)
	}
	hash, err := Broadcast(context.Background(), srv.URL, []byte{1, 2, 3})
	if err != nil || hash != "ABCDEF" || posted == "" {
		t.Fatalf("broadcast %q %v", hash, err)
	}
}

func TestBroadcast_nonzeroCodeIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"tx_response":{"code":5,"raw_log":"insufficient fee"}}`))
	}))
	defer srv.Close()
	if _, err := Broadcast(context.Background(), srv.URL, []byte{1}); err == nil {
		t.Fatal("a rejected tx was accepted")
	}
}
