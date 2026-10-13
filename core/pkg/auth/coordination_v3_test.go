package auth

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

// servedOn is r as a net/http server listening on localPort would hand it to a
// handler: the connection's local address is in the context.
func servedOn(r *http.Request, localPort string) *http.Request {
	port, err := strconv.Atoi(localPort)
	if err != nil {
		panic(err)
	}
	addr := &net.TCPAddr{IP: net.ParseIP("10.0.0.1"), Port: port}
	return r.WithContext(context.WithValue(r.Context(), http.LocalAddrContextKey, net.Addr(addr)))
}

func signedForPort(t *testing.T, key []byte, url string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, url, bytes.NewReader([]byte(`{"root":"x"}`)))
	if err := SignCoordination(key, r, time.Now(), testAudience); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestCheckCoordination_v3IsGoodForTheProcessItWasSignedFor(t *testing.T) {
	key, now := v2Key(t), time.Now()
	r := servedOn(signedForPort(t, key, "http://10.0.0.1:10104/v1/internal/secrets/reencrypt"), "10104")
	if v, ok := CheckCoordination(key, r, now, testAudience); !ok || v != CoordinationV3 {
		t.Fatalf("got version %d ok=%v, want v3 true", v, ok)
	}
}

// The index gateway and a namespace gateway on one node share the peer id but
// not the nonce cache: a stamp for one must not verify at the other.
func TestCheckCoordination_v3IsRefusedByASiblingProcessOnTheSameNode(t *testing.T) {
	key, now := v2Key(t), time.Now()
	r := servedOn(signedForPort(t, key, "http://10.0.0.1:10104/v1/internal/secrets/reencrypt"), "10010")
	if VerifyCoordinationV2(key, r, now, testAudience) {
		t.Fatal("a stamp for the process on port 10104 verified at the process on port 10010")
	}
}

func TestCheckCoordination_theHostHeaderDoesNotChooseTheProcess(t *testing.T) {
	key, now := v2Key(t), time.Now()
	r := servedOn(signedForPort(t, key, "http://10.0.0.1:10104/x"), "10010")
	r.Host = "10.0.0.1:10104"
	if VerifyCoordinationV2(key, r, now, testAudience) {
		t.Fatal("a replayer chose the process through the Host header")
	}
}

func TestCheckCoordination_aFailedV3IsNotRetriedAsV2(t *testing.T) {
	key, now := v2Key(t), time.Now()
	r := servedOn(signedForPort(t, key, "http://10.0.0.1:10104/x"), "10010")
	if _, ok := CheckCoordination(key, r, now, testAudience); ok {
		t.Fatal("a v3 stamp for another process verified through its v2 stamp")
	}
}

func TestCheckCoordination_aV2MACPutInTheV3HeaderIsRefused(t *testing.T) {
	key, now := v2Key(t), time.Now()
	r := servedOn(signedForPort(t, key, "http://10.0.0.1:10104/x"), "10104")
	r.Header.Set(CoordinationMACV3Header, r.Header.Get(CoordinationMACV2Header))
	if VerifyCoordinationV2(key, r, now, testAudience) {
		t.Fatal("the v2 MAC verified as a v3 MAC")
	}
}

// Rolling upgrade: a node on the previous build stamps v2 and v1 only.
func TestCheckCoordination_aV2OnlyStampFromAnOldNodeIsAcceptedInTheMixedWindow(t *testing.T) {
	if !AcceptLegacyCoordinationV2 {
		t.Skip("the v2 stamp is no longer accepted")
	}
	key, now := v2Key(t), time.Now()
	r := servedOn(signedForPort(t, key, "http://10.0.0.1:10104/x"), "10104")
	r.Header.Del(CoordinationMACV3Header)
	if v, ok := CheckCoordination(key, r, now, testAudience); !ok || v != CoordinationV2 {
		t.Fatalf("got version %d ok=%v, want v2 true", v, ok)
	}
}

func TestCheckCoordination_theNonceIsSpentOnceAcrossV3AndV2(t *testing.T) {
	key, now := v2Key(t), time.Now()
	r := servedOn(signedForPort(t, key, "http://10.0.0.1:10104/x"), "10104")
	if !VerifyCoordinationV2(key, r, now, testAudience) {
		t.Fatal("first use refused")
	}
	replay := servedOn(signedForPort(t, key, "http://10.0.0.1:10104/x"), "10104")
	replay.Header = r.Header.Clone()
	replay.Header.Del(CoordinationMACV3Header)
	if VerifyCoordinationV2(key, replay, now, testAudience) {
		t.Fatal("the nonce of a verified v3 stamp was accepted again through its v2 stamp")
	}
}

func TestRequestPort_defaultsAndNone(t *testing.T) {
	for url, want := range map[string]string{
		"http://h:6001/x": "6001",
		"http://h/x":      "80",
		"https://h/x":     "443",
		"/x":              "",
	} {
		r, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := requestPort(r); got != want {
			t.Errorf("requestPort(%q) = %q, want %q", url, got, want)
		}
	}
}
