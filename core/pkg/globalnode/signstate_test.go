package globalnode

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseSignState_readsCometBFTFormat(t *testing.T) {
	got, err := ParseSignState([]byte(`{"height":"123","round":2,"step":3,"signature":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got != (SignState{Height: 123, Round: 2, Step: 3}) {
		t.Fatalf("state = %v", got)
	}
}

func TestParseSignState_refusals(t *testing.T) {
	for _, bad := range []string{
		``,
		`{}`,
		`{"height":123,"round":0,"step":0}`,
		`{"height":"-1","round":0,"step":0}`,
		`{"height":"12","round":-1,"step":0}`,
		`{"height":"x","round":0,"step":0}`,
		`{"height":"1","round":0}`,
	} {
		if _, err := ParseSignState([]byte(bad)); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func TestCheckNotBehind_ordersHeightRoundStep(t *testing.T) {
	src := SignState{Height: 10, Round: 1, Step: 2}
	for _, ok := range []SignState{src, {10, 1, 3}, {10, 2, 0}, {11, 0, 0}} {
		if err := CheckNotBehind(ok, src); err != nil {
			t.Errorf("%v refused: %v", ok, err)
		}
	}
	for _, behind := range []SignState{{9, 9, 9}, {10, 0, 3}, {10, 1, 1}, {}} {
		if err := CheckNotBehind(behind, src); err == nil {
			t.Errorf("%v accepted behind %v", behind, src)
		}
	}
}

func TestValidatorKeyPubKey_refusesWrongSizes(t *testing.T) {
	for _, bad := range []string{
		`{"pub_key":{"type":"tendermint/PubKeyEd25519","value":"AAAA"},"priv_key":{"type":"tendermint/PrivKeyEd25519","value":"AAAA"}}`,
		`not json`,
		`{}`,
	} {
		if _, err := ValidatorKeyPubKey([]byte(bad)); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func TestParseX25519Hex_lengthAndHex(t *testing.T) {
	if _, err := ParseX25519Hex(" 00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff\n"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "00", "zz112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"} {
		if _, err := ParseX25519Hex(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func TestPollHTTP_returnsOnceTheRPCAnswers(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if hits.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := pollHTTP(ctx, srv.URL, 10*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 3 {
		t.Fatalf("%d probes, want 3", hits.Load())
	}
}

func TestPollHTTP_budgetExpiresWithTheLastError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := pollHTTP(ctx, srv.URL, 10*time.Millisecond); err == nil {
		t.Fatal("poll succeeded against a failing RPC")
	}
}
