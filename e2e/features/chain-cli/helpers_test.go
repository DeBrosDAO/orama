//go:build e2e_fleet

package chaincli

import (
	"fmt"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
)

// The environment's gateway proxies /v1/chain/ to the chain RPC, REST API and
// indexer of the node it lands on: loopback ports on a node that is not
// co-located (core/pkg/gateway/handlers/chainread ConfigFromEnv), which on
// every fleet node is the run's own co-hosted validator (chain-deploy.sh).
// So the gateway variants below read the run chain, whichever node answers.

// deadRPC is an address nothing listens on: the CLI's read fails.
const deadRPC = "http://127.0.0.1:1"

// readerNode is the validator whose loopback RPC and REST API the tests read.
const readerNode = 0

// rpcURL is validator readerNode's CometBFT RPC through an SSH tunnel: the
// chain listens on the node's loopback only.
func rpcURL(t *testing.T, c *chain.Chain) string {
	t.Helper()
	return "http://" + c.Tunnel(t, c.Node(t, readerNode), chain.RPCPort)
}

// restURL is the same node's Cosmos REST API through a tunnel.
func restURL(t *testing.T, c *chain.Chain) string {
	t.Helper()
	return "http://" + c.Tunnel(t, c.Node(t, readerNode), chain.APIPort)
}

// run is `orama <args>` as the operator's runner.
func run(t *testing.T, args ...string) oramacli.Result {
	t.Helper()
	return infra.Run(t, harness.CLI(t), args...)
}

// runJSON runs a command that must exit 0 and print one JSON value.
func runJSON(t *testing.T, v any, args ...string) {
	t.Helper()
	res := run(t, args...)
	infra.ExpectExit(t, res, infra.ExitOK)
	if err := oramacli.DecodeJSON(res, v); err != nil {
		t.Fatal(err)
	}
}

// find returns the first member named key at any depth of a decoded JSON
// value: the gateway proxy and the RPC wrap the same answer differently.
func find(v any, key string) (any, bool) {
	switch x := v.(type) {
	case map[string]any:
		if val, ok := x[key]; ok {
			return val, true
		}
		for _, child := range x {
			if val, ok := find(child, key); ok {
				return val, true
			}
		}
	case []any:
		for _, child := range x {
			if val, ok := find(child, key); ok {
				return val, true
			}
		}
	}
	return nil, false
}

// findString is find for a string member.
func findString(t *testing.T, v any, key string) string {
	t.Helper()
	val, ok := find(v, key)
	s, isString := val.(string)
	if !ok || !isString {
		t.Fatalf("the answer has no string member %q: %v", key, v)
	}
	return s
}

// requireRead fails unless res is a failed read (exit 1) whose output has every fragment.
func requireRead(t *testing.T, res oramacli.Result, fragments ...string) {
	t.Helper()
	infra.ExpectExit(t, res, infra.ExitFailure, fragments...)
}

// requireUsage fails unless res is a usage error (exit 2) with every fragment.
func requireUsage(t *testing.T, res oramacli.Result, fragments ...string) {
	t.Helper()
	infra.ExpectExit(t, res, infra.ExitUsage, fragments...)
}

func heightOf(t *testing.T, status any) int64 {
	t.Helper()
	var h int64
	if _, err := fmt.Sscan(findString(t, status, "latest_block_height"), &h); err != nil {
		t.Fatalf("latest_block_height is not a number: %v", err)
	}
	return h
}

// amountOf reads the string member key of a decoded answer as an integer.
func amountOf(t *testing.T, v any, key string) chain.Int {
	t.Helper()
	var n chain.Int
	if _, ok := n.SetString(findString(t, v, key), 10); !ok {
		t.Fatalf("member %q is not an integer: %v", key, v)
	}
	return n
}
