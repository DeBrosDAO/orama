//go:build e2e_fleet

package chain

import (
	"fmt"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// partitionTagBytes is the random bytes that make one partition's rules its
// own.
const partitionTagBytes = 4

// netChains are the netfilter chains the chain's peer traffic crosses on the
// host: INPUT and OUTPUT for a validator on the host (a fleet run, whose
// chain peers over the WireGuard overlay), FORWARD for one inside the
// orama-global namespace (stagenet, whose chain peers over the public network
// through the namespace's veth, website/src/docs/blockchain/run-a-global-node.mdx).
var netChains = []string{"INPUT", "OUTPUT", "FORWARD"}

// netTools are the firewall tools, one per address family: a peer reachable
// over IPv6 must not keep the cut from being one. -w waits for the xtables
// lock, which the node's own firewall and parallel tests hold now and then.
var netTools = []string{"iptables -w", "ip6tables -w"}

// partitionRule is one rule spec (without -I/-C/-D) and the tool that owns it.
type partitionRule struct {
	tool, spec string
}

// partitionRules are the rules that drop the chain's peer-to-peer TCP (port
// P2PPort, either side of the connection) in every chain of netChains and for
// both address families, tagged with the run so the runner's sweep after a
// destructive package (fleet.RestoreNodes) removes what a cleanup could not.
// SSH, the cluster and every other port are untouched.
func partitionRules(runID, tag string) []partitionRule {
	comment := "-m comment --comment " + fleet.ShellQuote("e2e-"+runID+"-"+tag)
	var rules []partitionRule
	for _, tool := range netTools {
		for _, chain := range netChains {
			for _, side := range []string{"--dport", "--sport"} {
				rules = append(rules, partitionRule{tool: tool, spec: fmt.Sprintf("%s -p tcp %s %d %s -j DROP", chain, side, P2PPort, comment)})
			}
		}
	}
	return rules
}

// remove deletes every copy of the rule and proves it is gone.
func (r partitionRule) remove() string { return fleet.RemoveRuleCommand(r.tool, r.spec) }

// Partition cuts n off from every other validator: it drops the chain's p2p
// TCP in both directions, at the host's firewall (partitionRules). The
// cleanup, registered before the first rule is inserted, deletes every rule
// and proves none is left, so an interrupted insert leaves nothing behind. The
// returned function heals the partition at once; the cleanup then finds
// nothing to do.
func (c *Chain) Partition(t testing.TB, n fleet.Node) (heal func()) {
	t.Helper()
	rules := partitionRules(c.F.State.RunID, randomHex(t, partitionTagBytes))
	remove := func() {
		for _, r := range rules {
			c.CleanupExec(t, n, r.remove())
		}
	}
	t.Cleanup(remove)
	for _, r := range rules {
		c.MustRun(t, n, QueryBudget, r.tool+" -I "+r.spec)
	}
	return remove
}

// PeerCount is how many peers n's CometBFT reports connected.
func (c *Chain) PeerCount(t testing.TB, n fleet.Node) (int, error) {
	t.Helper()
	var r struct {
		NPeers Int `json:"n_peers"`
	}
	if err := c.Comet(t, n, "/net_info", &r); err != nil {
		return 0, err
	}
	return int(r.NPeers.Int64()), nil
}
