//go:build e2e_fleet

package chain

import (
	"fmt"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/config"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

// FleetHost is where a fleet run's validator serves RPC, REST and gRPC:
// its own loopback (e2e/scripts/chain-deploy.sh).
const FleetHost = "127.0.0.1"

// Host is the address, on the node itself, where the chain's node-local
// ports answer. A fleet run's validator listens on loopback. Stagenet's chain
// is co-located inside the orama-global netns and answers on
// config.StagenetChainHost from the host's root namespace (docs/RUN_A_GLOBAL_NODE.md).
// The chain's home, unit, service user and binary are the same on both
// (Home, Unit, ServiceUser, Oramad).
func (c *Chain) Host() string {
	if c.F.State.IsStagenet() {
		return config.StagenetChainHost
	}
	return FleetHost
}

// RPC is the `--node` value of an oramad command run on a validator.
func (c *Chain) RPC() string { return fmt.Sprintf("tcp://%s:%d", c.Host(), RPCPort) }

// RPCHTTP is the CometBFT RPC's HTTP base URL on the validator.
func (c *Chain) RPCHTTP() string { return fmt.Sprintf("http://%s:%d", c.Host(), RPCPort) }

// RequireFreshChain skips (not covered) on the stagenet target, for a test
// whose premise is the genesis of a run chain: zero supply, an empty
// shielded pool, a validator operator with no fee balance, the E2E short
// epoch. Stagenet is a long-lived chain whose state has moved past all of it.
func RequireFreshChain(t testing.TB) {
	t.Helper()
	requireFresh(t, harness.Fleet(t).State)
}

func requireFresh(t testing.TB, st *fleet.State) {
	t.Helper()
	if st.IsStagenet() {
		harness.SkipNotApplicable(t, "the stagenet chain is long-lived, not a fresh run chain: its supply, pools, fee balances and epoch length are no longer the deploy script's genesis")
	}
}

// checkChainID accepts a devnet chain id in a fleet run and a stagenet chain
// id (config.CheckStagenetChainID) on the stagenet target: nothing else is
// ever signed for.
func checkChainID(st *fleet.State, id string) error {
	if st.IsStagenet() {
		return config.CheckStagenetChainID(id)
	}
	if !strings.Contains(id, DevnetMarker) {
		return fmt.Errorf("every run chain is a devnet chain (id contains %s)", DevnetMarker)
	}
	return nil
}

// IndexerGateway is the run's public gateway for /v1/chain/index/. A gateway proxies the indexer of
// its own node. A fleet run installs one beside every validator (e2e/scripts/chain-deploy.sh), so
// any gateway answers; stagenet runs one on a single node (chain/scripts/stagenet/deploy.sh), so
// the client is pinned to that node's gateway and the DNS answer cannot pick one without it.
func (c *Chain) IndexerGateway(t testing.TB) *gw.Client {
	t.Helper()
	g := harness.GW(t)
	if ip := indexerPin(c.F.State); ip != "" {
		return g.PinTo(ip)
	}
	return g
}

// indexerPin is the address IndexerGateway pins to, or "" when any gateway has an indexer.
func indexerPin(st *fleet.State) string {
	if st.IsStagenet() {
		return config.StagenetIndexerIP()
	}
	return ""
}
