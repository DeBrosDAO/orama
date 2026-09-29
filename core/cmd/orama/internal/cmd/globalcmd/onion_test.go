package globalcmd

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/clusterreg"
	"github.com/spf13/cobra"
)

const testOnion = "abcdefghijklmnopqrstuvwxyz234567abcdefghijklmnopqrstuvwx.onion"

func onionCmd(t *testing.T, args ...string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "test"}
	AddOnionFlags(cmd.Flags())
	cmd.Flags().Uint64("account-number", 0, "")
	cmd.Flags().Uint64("sequence", 0, "")
	if err := cmd.ParseFlags(args); err != nil {
		t.Fatal(err)
	}
	return cmd
}

// deadSOCKS returns the address of a port nothing listens on.
func deadSOCKS(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

func TestChainTarget_withoutOnionIsUnchanged(t *testing.T) {
	t.Setenv(OnionEnv, "")
	ctx := context.Background()
	got, node, err := chainTarget(onionCmd(t), ctx, "http://127.0.0.1:31003")
	if err != nil || node != "http://127.0.0.1:31003" || got != ctx {
		t.Fatalf("got node %q, err %v, ctx changed %v", node, err, got != ctx)
	}
}

func TestChainTarget_onionReplacesNode(t *testing.T) {
	t.Setenv(OnionEnv, "")
	_, node, err := chainTarget(onionCmd(t, "--onion", testOnion+":31003"), context.Background(), "")
	if err != nil || node != "http://"+testOnion+":31003" {
		t.Fatalf("node = %q, err = %v", node, err)
	}
}

func TestChainTarget_environmentSuppliesOnion(t *testing.T) {
	t.Setenv(OnionEnv, testOnion)
	_, node, err := chainTarget(onionCmd(t), context.Background(), "")
	if err != nil || node != "http://"+testOnion+":80" {
		t.Fatalf("node = %q, err = %v", node, err)
	}
}

func TestChainTarget_nodeAndOnionTogetherIsAUsageError(t *testing.T) {
	t.Setenv(OnionEnv, "")
	_, _, err := chainTarget(onionCmd(t, "--onion", testOnion), context.Background(), "http://127.0.0.1:31003")
	if err == nil || !strings.Contains(err.Error(), "pass one") {
		t.Fatalf("err = %v, want a two-routes usage error", err)
	}
}

func TestChainTarget_clearnetHostIsRefusedAsOnion(t *testing.T) {
	t.Setenv(OnionEnv, "")
	_, _, err := chainTarget(onionCmd(t, "--onion", "chain.example.com"), context.Background(), "")
	if err == nil || !strings.Contains(err.Error(), "onion") {
		t.Fatalf("err = %v, want a refused address", err)
	}
}

// With Tor unreachable, SubmitDirect fails before signing and nothing goes out
// on the clearnet: the default transport is trapped and no --node exists.
func TestSubmitDirect_onionWithTorDownFailsWithoutClearnet(t *testing.T) {
	t.Setenv(OnionEnv, "")
	old := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Errorf("clearnet request: %s", r.URL)
		return nil, errors.New("trapped")
	})
	defer func() { http.DefaultTransport = old }()

	cmd := onionCmd(t, "--onion", testOnion, "--onion-socks", deadSOCKS(t))
	cmd.SetContext(context.Background())
	operator := "orama19rl4cm2hmr8afy4kldpxz3fka4jguq0a5tup0s"
	in := clusterreg.Direct{
		TypeURL: clusterreg.RegisterClusterTypeURL, Msg: []byte{1}, FeeAmount: "1", Gas: 1, ChainID: "orama-test",
	}
	err := SubmitDirect(cmd, operator, "", "", 0, 0, in, "registered")
	if err == nil || !strings.Contains(err.Error(), "nothing was tried outside Tor") {
		t.Fatalf("err = %v, want the Tor-unreachable error", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
