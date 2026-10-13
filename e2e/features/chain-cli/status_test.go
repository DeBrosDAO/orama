//go:build e2e_fleet

package chaincli

import (
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
)

// TestChainStatus_readsRPCAndGateway: `orama chain status` prints CometBFT's
// status of the run's chain, through --rpc and through the gateway's
// /v1/chain/ proxy (the active environment's): the network id is the run's
// chain id, a block has been committed and the node is not catching up; a
// dead --rpc is a failed read, not an empty answer.
func TestChainStatus_readsRPCAndGateway(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	for name, args := range map[string][]string{
		"the node's RPC":      {"chain", "status", "--rpc", rpcURL(t, c)},
		"the gateway's proxy": {"chain", "status"},
	} {
		var status any
		runJSON(t, &status, args...)
		if got := findString(t, status, "network"); got != c.ID {
			t.Errorf("%s: network %q, want the run's chain %q", name, got, c.ID)
		}
		if h := heightOf(t, status); h <= 0 {
			t.Errorf("%s: latest block height %d", name, h)
		}
		if catching, _ := find(status, "catching_up"); catching != false {
			t.Errorf("%s: catching_up is %v", name, catching)
		}
	}
	requireRead(t, run(t, "chain", "status", "--rpc", deadRPC), "read")
	requireUsage(t, run(t, "chain", "status", "surplus"))
}

// TestChainValidator_listsTheSetAndShowsOne: without an argument the command
// lists the CometBFT validator set (through --rpc and through the gateway):
// the run's three co-hosted validators, each with voting power; with an
// oramavaloper address it shows that validator's bonded staking record from
// --node's REST API. A malformed address is a usage error and an address
// with no validator a failed read.
func TestChainValidator_listsTheSetAndShowsOne(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	for name, args := range map[string][]string{
		"the node's RPC":      {"chain", "validator", "--rpc", rpcURL(t, c)},
		"the gateway's proxy": {"chain", "validator"},
	} {
		var set any
		runJSON(t, &set, args...)
		val, _ := find(set, "validators")
		list, _ := val.([]any)
		if len(list) != len(c.Nodes()) {
			t.Errorf("%s: %d validators, want the run's %d", name, len(list), len(c.Nodes()))
		}
	}
	rest := restURL(t, c)
	valoper := c.Valoper(t, c.Validator(t, c.Node(t, readerNode)))
	var one any
	runJSON(t, &one, "chain", "validator", valoper, "--node", rest)
	if got := findString(t, one, "operator_address"); got != valoper {
		t.Errorf("validator %s: the record is for %q", valoper, got)
	}
	if got := findString(t, one, "status"); got != "BOND_STATUS_BONDED" {
		t.Errorf("validator %s is %s, want BOND_STATUS_BONDED", valoper, got)
	}
	stranger := c.Valoper(t, c.NewKey(t, c.Node(t, readerNode), "e2e-cli-stranger"))
	requireRead(t, run(t, "chain", "validator", stranger, "--node", rest), "answered HTTP")
	requireUsage(t, run(t, "chain", "validator", "not-a-valoper"), "oramavaloper")
	requireRead(t, run(t, "chain", "validator", valoper), "--node")
}
