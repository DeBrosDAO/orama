package chainread

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSimulate_postsTheBase64TxAndDecodesTheAnswer(t *testing.T) {
	var gotPath, gotType, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotPath, gotType, gotBody = r.Method+" "+r.URL.Path, r.Header.Get("Content-Type"), string(b)
		_, _ = w.Write([]byte(`{"gas_wanted":"200000","gas_used":"120000","fee":{"denom":"norama","amount":"240000"},"base_fee":"2"}`))
	}))
	t.Cleanup(srv.Close)
	got, err := (&Reader{Gateway: srv.URL}).Simulate(context.Background(), []byte("abc"))
	if err != nil {
		t.Fatal(err)
	}
	if got.GasUsed != 120000 || got.GasWanted != 200000 || got.Fee.Amount != "240000" || got.Fee.Denom != "norama" || got.BaseFee != "2" {
		t.Fatalf("result %+v", got)
	}
	if gotPath != "POST /v1/chain/simulate" || gotType != "application/json" {
		t.Errorf("sent %s %s", gotPath, gotType)
	}
	var sent map[string]string
	if err := json.Unmarshal([]byte(gotBody), &sent); err != nil || sent["tx_bytes"] != base64.StdEncoding.EncodeToString([]byte("abc")) {
		t.Errorf("body %q", gotBody)
	}
}

func TestSimulate_gasIsADecimalStringAndABareNumberIsRefused(t *testing.T) {
	answer := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(answer))
	}))
	t.Cleanup(srv.Close)
	r := &Reader{Gateway: srv.URL}

	answer = `{"gas_wanted":"18446744073709551615","gas_used":"9007199254740993","fee":{"denom":"norama","amount":"1"},"base_fee":"1"}`
	got, err := r.Simulate(context.Background(), []byte("abc"))
	if err != nil || got.GasWanted != math.MaxUint64 || got.GasUsed != 9007199254740993 {
		t.Fatalf("a gas figure above 2^53 was read as %+v (err %v)", got, err)
	}

	answer = `{"gas_wanted":200000,"gas_used":120000,"fee":{"denom":"norama","amount":"1"},"base_fee":"1"}`
	if _, err := r.Simulate(context.Background(), []byte("abc")); err == nil {
		t.Fatal("a bare JSON number for a 64-bit gas figure was accepted")
	}
}

func TestBroadcast_aRefusalIsATypedError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"code":32,"codespace":"sdk","log":"account sequence mismatch","tx_hash":"AB"}`))
	}))
	t.Cleanup(srv.Close)
	_, err := (&Reader{Gateway: srv.URL}).Broadcast(context.Background(), []byte("abc"))
	var refused *TxRefusedError
	if !errors.As(err, &refused) || refused.Code != 32 || refused.Codespace != "sdk" || refused.TxHash != "AB" {
		t.Fatalf("err = %v", err)
	}
}

func TestBroadcast_otherFailures(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "too many", http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)
	if _, err := (&Reader{Gateway: srv.URL}).Broadcast(context.Background(), []byte("abc")); err == nil || errors.As(err, new(*TxRefusedError)) {
		t.Fatalf("err = %v, want a plain HTTP failure", err)
	}
	if _, err := (&Reader{}).Simulate(context.Background(), []byte("abc")); err == nil {
		t.Fatal("simulate without a gateway succeeded")
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte("nope"))
	}))
	t.Cleanup(bad.Close)
	if _, err := (&Reader{Gateway: bad.URL}).Simulate(context.Background(), []byte("abc")); err == nil {
		t.Fatal("a 422 that is not a refusal was accepted")
	}
}
