package upgrade

import (
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/pkg/cli/remotessh"
	"github.com/DeBrosOfficial/network/pkg/inspector"
)

// RemoteUpgrader handles rolling upgrades across remote nodes.
type RemoteUpgrader struct {
	flags *Flags
}

// NewRemoteUpgrader creates a new remote upgrader.
func NewRemoteUpgrader(flags *Flags) *RemoteUpgrader {
	return &RemoteUpgrader{flags: flags}
}

// Execute runs the remote rolling upgrade.
func (r *RemoteUpgrader) Execute() error {
	nodes, err := remotessh.LoadEnvNodes(r.flags.Env)
	if err != nil {
		return err
	}

	// Filter to single node if specified
	if r.flags.NodeFilter != "" {
		nodes = remotessh.FilterByIP(nodes, r.flags.NodeFilter)
		if len(nodes) == 0 {
			return fmt.Errorf("node %s not found in %s environment", r.flags.NodeFilter, r.flags.Env)
		}
	}

	fmt.Printf("Rolling upgrade: %s (%d nodes, %ds delay)\n\n", r.flags.Env, len(nodes), r.flags.Delay)

	// Print execution plan
	for i, node := range nodes {
		fmt.Printf("  %d. %s (%s)\n", i+1, node.Host, node.Role)
	}
	fmt.Println()

	for i, node := range nodes {
		fmt.Printf("[%d/%d] Upgrading %s (%s)...\n", i+1, len(nodes), node.Host, node.Role)

		if err := r.upgradeNode(node); err != nil {
			return fmt.Errorf("upgrade failed on %s: %w\nStopping rollout — remaining nodes not upgraded", node.Host, err)
		}

		fmt.Printf("  ✓ %s upgraded\n", node.Host)

		// Wait between nodes (except after the last one)
		if i < len(nodes)-1 && r.flags.Delay > 0 {
			fmt.Printf("  Waiting %ds before next node...\n\n", r.flags.Delay)
			time.Sleep(time.Duration(r.flags.Delay) * time.Second)
		}
	}

	fmt.Printf("\n✓ Rolling upgrade complete (%d nodes)\n", len(nodes))
	return nil
}

// upgradeNode runs `orama node upgrade --restart` on a single remote node.
func (r *RemoteUpgrader) upgradeNode(node inspector.Node) error {
	sudo := remotessh.SudoPrefix(node)
	cmd := fmt.Sprintf("%sorama node upgrade --restart", sudo)
	return remotessh.RunSSHStreaming(node, cmd)
}
