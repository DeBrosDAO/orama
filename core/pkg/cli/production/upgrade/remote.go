package upgrade

import (
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/pkg/cli/noderesolver"
	"github.com/DeBrosOfficial/network/pkg/cli/remotessh"
	"github.com/DeBrosOfficial/network/pkg/inspector"
	"github.com/DeBrosOfficial/network/pkg/rollout"
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
	nodes, err := noderesolver.ResolveNodes(r.flags.Env)
	if err != nil {
		return err
	}

	cleanup, err := remotessh.PrepareNodeKeys(nodes)
	if err != nil {
		return err
	}
	defer cleanup()

	// Filter to single node if specified
	if r.flags.NodeFilter != "" {
		nodes = remotessh.FilterByIP(nodes, r.flags.NodeFilter)
		if len(nodes) == 0 {
			return fmt.Errorf("node %s not found in %s environment", r.flags.NodeFilter, r.flags.Env)
		}
	}

	// Build the plan from what the cluster is actually doing, not from the
	// order nodes happen to appear in nodes.conf. Restarting the leader first
	// costs an election on every node after it; restarting two nameservers back
	// to back takes the zone offline.
	fmt.Printf("Reading cluster state from %d nodes...\n", len(nodes))
	roles := rollout.ReadRoles(nodes, rollout.DefaultRunner)

	plan, err := rollout.Build(nodes, roles)
	if err != nil {
		return fmt.Errorf("cannot plan a rolling upgrade of %s: %w", r.flags.Env, err)
	}

	fmt.Printf("\n%s\n", plan)

	// A single-node filter is a targeted repair, not a rollout, and the plan
	// above does not describe it. Confirmation still applies.
	if !r.flags.Yes {
		return fmt.Errorf("re-run with --yes to execute this plan")
	}

	for i, step := range plan.Steps {
		fmt.Printf("[%d/%d] Upgrading %s (%s, %s)...\n",
			i+1, len(plan.Steps), step.Node.Host, step.Node.Role, step.Role)

		if err := r.upgradeNode(step.Node); err != nil {
			return fmt.Errorf("upgrade failed on %s: %w\nStopping rollout — %d node(s) not upgraded",
				step.Node.Host, err, len(plan.Steps)-i-1)
		}
		fmt.Printf("  ✓ %s upgraded\n", step.Node.Host)

		// Gate on the node actually rejoining, not on a fixed sleep. A sleep
		// cannot tell a node that came back in 20 seconds from one that never
		// came back, so the rollout restarted the next voter either way — which
		// is how a rolling upgrade takes out a quorum.
		if i < len(plan.Steps)-1 {
			fmt.Printf("  Waiting for %s to rejoin the cluster...\n", step.Node.Host)
			if err := rollout.WaitReady(step.Node, rollout.DefaultRunner, r.gateBudget()); err != nil {
				return fmt.Errorf("%w\nStopping rollout — %d node(s) not upgraded. "+
					"The cluster still has its remaining voters; fix this node before continuing",
					err, len(plan.Steps)-i-1)
			}
			fmt.Printf("  ✓ %s is carrying its share again\n\n", step.Node.Host)
		}
	}

	fmt.Printf("\n✓ Rolling upgrade complete (%d nodes)\n", len(plan.Steps))
	return nil
}

// gateBudget is how long one node has to rejoin before the rollout stops.
func (r *RemoteUpgrader) gateBudget() time.Duration {
	if r.flags.Delay > 0 {
		return time.Duration(r.flags.Delay) * time.Second
	}
	return rollout.GateBudget
}

// upgradeNode runs `orama node upgrade --restart` on a single remote node,
// forwarding the per-node flags the operator passed locally (--nameserver,
// --force, --skip-checks) so the remote orchestrator sees the same intent.
// Without this forwarding, the remote command would always use the saved
// preference, silently dropping operator overrides on the floor.
func (r *RemoteUpgrader) upgradeNode(node inspector.Node) error {
	sudo := remotessh.SudoPrefix(node)
	cmd := fmt.Sprintf("%sorama node upgrade --restart", sudo)

	// Tri-state pointer flag: forward only when explicitly set locally.
	// nil = "honor saved preference on the remote" — don't pass anything.
	if r.flags.Nameserver != nil {
		if *r.flags.Nameserver {
			cmd += " --nameserver"
		} else {
			cmd += " --nameserver=false"
		}
	}

	// Plain booleans: forward when true. False is the default everywhere
	// so no need to send `=false` explicitly.
	if r.flags.Force {
		cmd += " --force"
	}
	if r.flags.SkipChecks {
		cmd += " --skip-checks"
	}

	return remotessh.RunSSHStreaming(node, cmd)
}
