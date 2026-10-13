package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func liveID(id string, err error) chainIDReader {
	return func(context.Context) (string, error) { return id, err }
}

// A reset gives stagenet a new chain id; the target used to write a remembered
// default (orama-stagenet-1) and every chain assertion of the run then failed
// against the running chain (orama-stagenet-5).
func TestResolveChainID_followsTheRunningChain(t *testing.T) {
	got, err := resolveChainID(context.Background(), "", liveID("orama-stagenet-5", nil))
	if err != nil || got != "orama-stagenet-5" {
		t.Fatalf("got %q, %v; want the running chain's id", got, err)
	}
	if got, err := resolveChainID(context.Background(), "orama-stagenet-5", liveID("orama-stagenet-5", nil)); err != nil || got != "orama-stagenet-5" {
		t.Fatalf("a matching --chain-id: %q, %v", got, err)
	}
}

func TestResolveChainID_refusals(t *testing.T) {
	for name, tc := range map[string]struct {
		flag string
		live chainIDReader
		want string
	}{
		"a --chain-id that is not the running chain": {"orama-stagenet-1", liveID("orama-stagenet-5", nil), "does not match the running chain"},
		"a live id that is not a stagenet id":        {"", liveID("orama-testnet-1", nil), "refuses"},
		"the gateway cannot be read":                 {"", liveID("", errors.New("connection refused")), "connection refused"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := resolveChainID(context.Background(), tc.flag, tc.live); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestParseChainStatusNetwork(t *testing.T) {
	got, err := parseChainStatusNetwork([]byte(`{"jsonrpc":"2.0","id":-1,"result":{"node_info":{"network":"orama-stagenet-5","moniker":"gengar"}}}`))
	if err != nil || got != "orama-stagenet-5" {
		t.Fatalf("got %q, %v", got, err)
	}
	for _, body := range []string{``, `{}`, `{"result":{"node_info":{"network":""}}}`, `not json`} {
		if _, err := parseChainStatusNetwork([]byte(body)); err == nil {
			t.Errorf("%q: want an error", body)
		}
	}
}
