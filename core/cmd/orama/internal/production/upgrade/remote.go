package upgrade

import (
	"encoding/base64"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/noderesolver"
	"github.com/DeBrosOfficial/network/pkg/archivetrust"
	"github.com/DeBrosOfficial/network/pkg/inspector"
	oramainstall "github.com/DeBrosOfficial/network/pkg/install"
	"github.com/DeBrosOfficial/network/pkg/remotessh"
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

// upgradeNode runs the upgrade on a single remote node.
func (r *RemoteUpgrader) upgradeNode(node inspector.Node) error {
	return remotessh.RunSSHStreaming(node, upgradeCommand(remotessh.SudoPrefix(node), r.flags))
}

// stagedPaths are what the node-side guard checks before it runs the staged
// CLI as root: the archive tree, its bin directory, the CLI, and the trust
// anchor stage-archive verified it against.
type stagedPaths struct {
	base, bin, cli, anchor string
}

// nodeStagedPaths are a node's.
var nodeStagedPaths = stagedPaths{
	base:   oramainstall.OramaBase,
	bin:    filepath.Dir(newOramaBinaryPath),
	cli:    newOramaBinaryPath,
	anchor: archivetrust.AnchorPath,
}

// upgradeCommand is `node upgrade --restart`, run as root with the CLI of the
// staged build (/opt/orama/bin/orama, which `orama push` verified and put in
// place), forwarding the per-node flags the operator passed locally
// (--nameserver, --force, --skip-checks) so the remote orchestrator sees the
// same intent.
//
// The staged CLI, not the one on the node's PATH: that one is the release
// being replaced, and everything it runs before handing over to the new
// binary — the pre-stop checks, the leadership hand-over, recording the raft
// identity, the stop itself — would be the old release's code. Upgrading from
// 0.122.x that code writes a recovery peers.json and stops the leader without
// a hand-over. Run from the staged build, the whole upgrade is the new
// release's code, and its re-exec has nothing to hand over to.
//
// Before running it, the node checks that it is running what a push staged:
// a trust anchor exists (a node that was never pushed a signed build has
// none, and its /opt/orama/bin/orama is whatever its old release extracted),
// and /opt/orama, its bin/ and the CLI are root's, not writable by anyone
// else, and not symlinks — a CLI anyone but root could have replaced is not
// run as root. The script travels base64-encoded into `bash -s`.
func upgradeCommand(sudo string, flags *Flags) string {
	script := upgradeScript(nodeStagedPaths, upgradeArgs(flags))
	return "printf %s " + base64.StdEncoding.EncodeToString([]byte(script)) + " | " + sudo + "bash -s"
}

// upgradeArgs are the arguments after the CLI.
func upgradeArgs(flags *Flags) string {
	args := "node upgrade --restart"

	// Tri-state pointer flag: forward only when explicitly set locally.
	// nil = "honor saved preference on the remote" — don't pass anything.
	if flags.Nameserver != nil {
		if *flags.Nameserver {
			args += " --nameserver"
		} else {
			args += " --nameserver=false"
		}
	}

	// Plain booleans: forward when true. False is the default everywhere
	// so no need to send `=false` explicitly.
	if flags.Force {
		args += " --force"
	}
	if flags.SkipChecks {
		args += " --skip-checks"
	}
	return args
}

// pushHint is what a node whose staged build cannot be trusted tells the
// operator.
const pushHint = "stage this release on it first: orama push --env <env> --archive <path> --trust-signers <0xWallet>"

// upgradeScript is the node-side guard, then the staged CLI with args.
func upgradeScript(p stagedPaths, args string) string {
	return strings.Join([]string{
		"set -eu",
		`fail() { echo "refusing to upgrade: $1; ` + pushHint + `" >&2; exit 1; }`,
		`[ -f ` + p.anchor + ` ] || fail "no trust anchor at ` + p.anchor + `, so nothing verified ` + p.cli + `"`,
		`for p in ` + p.base + ` ` + p.bin + ` ` + p.cli + `; do`,
		`  [ -e "$p" ] || fail "$p does not exist"`,
		`  [ ! -L "$p" ] || fail "$p is a symlink"`,
		// Fails closed: a find that cannot inspect the path prints nothing,
		// which must not read as "nothing wrong".
		`  bad=$(find "$p" -maxdepth 0 \( ! -user root -o -perm -020 -o -perm -002 \) -print) || fail "cannot inspect $p"`,
		`  [ -z "$bad" ] || fail "$p is not root's alone"`,
		`done`,
		`[ -f ` + p.cli + ` ] || fail "` + p.cli + ` is not a regular file"`,
		`exec ` + p.cli + ` ` + args,
	}, "\n") + "\n"
}
