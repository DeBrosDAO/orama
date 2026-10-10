package clusterreg

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestWithHTTPClient_routesRequestsThroughThatClientOnly(t *testing.T) {
	old := http.DefaultTransport
	http.DefaultTransport = rtFunc(func(r *http.Request) (*http.Response, error) {
		t.Errorf("request used the default transport: %s", r.URL)
		return nil, http.ErrUseLastResponse
	})
	defer func() { http.DefaultTransport = old }()

	var urls []string
	client := &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
		urls = append(urls, r.URL.String())
		body := `{"tx_response":{"code":0,"txhash":"AB"}}`
		if r.Method == http.MethodGet {
			body = `{"account":{"account_number":"1","sequence":"2"}}`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	})}
	ctx := WithHTTPClient(context.Background(), client)

	if _, err := FetchAccount(ctx, "http://x.onion", vectorAddress); err != nil {
		t.Fatal(err)
	}
	if _, err := Broadcast(ctx, "http://x.onion", []byte{1}); err != nil {
		t.Fatal(err)
	}
	if len(urls) != 2 {
		t.Fatalf("client saw %v", urls)
	}
}

func TestWithHTTPClient_failureIsReturnedNotRetried(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return nil, io.ErrUnexpectedEOF
	})}
	ctx := WithHTTPClient(context.Background(), client)
	if _, err := Broadcast(ctx, "http://x.onion", []byte{1}); err == nil {
		t.Fatal("a failed request was reported as sent")
	}
	if calls != 1 {
		t.Fatalf("client was called %d times, want 1", calls)
	}
}

func TestParseUint_refusesWhatWrapsAround(t *testing.T) {
	for in, want := range map[string]uint64{"": 0, "0": 0, "42": 42, "18446744073709551615": 18446744073709551615} {
		if got, err := parseUint(in); err != nil || got != want {
			t.Errorf("parseUint(%q) = %d, %v", in, got, err)
		}
	}
	for _, in := range []string{"18446744073709551616", "99999999999999999999999", "-1", "1.5", "0x10", "abc", "\x1b[2J"} {
		if _, err := parseUint(in); err == nil {
			t.Errorf("parseUint(%q) succeeded", in)
		} else if strings.ContainsRune(err.Error(), 0x1b) {
			t.Errorf("the error carries an escape: %q", err.Error())
		}
	}
}

func TestBroadcast_aMultiLineRefusalIsOnOneLine(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"tx_response":{"code":5,"raw_log":"insufficient fee\nbase fee 7\ngot 1"}}`))
	}))
	defer srv.Close()
	_, err := Broadcast(context.Background(), srv.URL, []byte{1})
	if err == nil || strings.Contains(err.Error(), "\n") || !strings.Contains(err.Error(), "insufficient fee | base fee 7 | got 1") {
		t.Fatalf("err = %q", err)
	}
}

func TestStatusError_aMultiLineMessageIsOnOneLine(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"rpc error:\n  code = Unknown\ndesc = boom\u001b[2J"}`, http.StatusInternalServerError)
	}))
	defer srv.Close()
	_, err := FetchAccount(context.Background(), srv.URL, vectorAddress)
	var status *StatusError
	if !errors.As(err, &status) {
		t.Fatalf("err = %v", err)
	}
	if want := "rpc error: | code = Unknown | desc = boom[2J"; status.Message != want {
		t.Fatalf("message = %q, want %q", status.Message, want)
	}
}

func TestNotSent_onlyADefiniteRefusalMeansNothingWasSent(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want bool
	}{
		"nil":                 {nil, false},
		"refused by CheckTx":  {fmt.Errorf("broadcast the transaction: %w (code 5): fee", ErrBroadcastRejected), true},
		"a bad request":       {&StatusError{Code: http.StatusBadRequest}, true},
		"not found":           {&StatusError{Code: http.StatusNotFound}, true},
		"a server error":      {&StatusError{Code: http.StatusBadGateway}, false},
		"a lost connection":   {errors.New("connection reset by peer"), false},
		"a deadline":          {context.DeadlineExceeded, false},
		"an unreadable reply": {errors.New("broadcast response is not JSON"), false},
	} {
		if got := NotSent(tc.err); got != tc.want {
			t.Errorf("%s: NotSent = %v, want %v", name, got, tc.want)
		}
	}
}

func TestBroadcast_aRefusalIsErrBroadcastRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"tx_response":{"code":5,"raw_log":"insufficient fee"}}`))
	}))
	defer srv.Close()
	_, err := Broadcast(context.Background(), srv.URL, []byte{1})
	if !errors.Is(err, ErrBroadcastRejected) || !NotSent(err) {
		t.Fatalf("err = %v", err)
	}
}
