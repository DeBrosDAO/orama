package install

import (
	"strconv"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

// The IPFS Cluster swarm is reachable only over the WireGuard mesh: no rule
// opens its port on its own, and the one rule that admits it is the overlay
// subnet arriving on wg0. Install binds the listener to the node's WireGuard address as well
// (installers.ClusterSwarmListenAddr), so this holds with the firewall down.
func TestFirewall_clusterSwarmOnlyOverWireGuard(t *testing.T) {
	fp := NewFirewallProvisioner(FirewallConfig{
		IsNameserver:   true,
		TURNEnabled:    true,
		TURNRelayStart: defaultTURNRelayPortStart,
		TURNRelayEnd:   defaultTURNRelayPortEnd,
	})
	port := strconv.Itoa(constants.IPFSClusterSwarmPort)
	overlay := "in on wg0 from " + constants.WireGuardSubnet
	sawOverlay := false
	for _, rule := range fp.DesiredAllowRules() {
		if rule == overlay {
			sawOverlay = true
			continue
		}
		if strings.HasPrefix(rule, port+"/") || strings.Contains(rule, "port "+port) {
			t.Errorf("rule %q opens the IPFS Cluster swarm port outside the mesh", rule)
		}
	}
	if !sawOverlay {
		t.Errorf("no %q rule: cluster peers could not reach the swarm over the mesh", overlay)
	}
}
