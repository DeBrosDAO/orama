//go:build e2e_fleet

package chain

import (
	"fmt"
	"strings"

	"github.com/DeBrosOfficial/network/e2e/harness/config"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
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
