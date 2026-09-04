// Package raftid performs the one-time move from address-derived raft node ids
// to stable, peer-id-based ones.
//
// rqlite defaults a node's raft id to its raft advertise address, so identity
// has been a function of routing: give the same machine a new overlay address
// and it mints a new raft id, joins as a second member, and the old entry stays
// in the configuration as a voter nothing can reach. Two such events on a
// five-voter cluster leave quorum at 3-of-7 with five live voters.
//
// rqlite has no way to rename a member in place — the only supported paths are
// remove-then-rejoin, or the peers.json disaster procedure — so this cannot be
// a side effect of an upgrade. A node that simply started passing -node-id
// would join as a NEW member and leave its old id behind: the exact failure the
// change exists to prevent, applied to every node at once. Hence a deliberate,
// serial, quorum-checked migration, one node at a time.
package raftid

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/cli/production/clusterops"
	"github.com/DeBrosOfficial/network/pkg/cli/remotessh"
	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/inspector"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// indexRQLiteDataDir is where the index rqlite keeps its raft state on a node.
// It mirrors IndexSupervisor.CoreRQLiteDir(), which derives it from the node's
// configured data dir; every deployed node uses the standard layout.
const indexRQLiteDataDir = "/opt/orama/.orama/data/rqlite"

// indexRQLiteEnvFile is the systemd env file the index rqlite unit reads its
// arguments from. -node-id and -join reach rqlited only through it.
const indexRQLiteEnvFile = "/opt/orama/.orama/data/namespaces/index/rqlite.env"

// rejoinTimeout bounds the wait for a migrated node to reappear in the raft
// configuration under its new id. A node has to restart rqlited, replay its
// join and catch up its log; a few minutes is generous for a healthy cluster
// and short enough that a stuck migration is not mistaken for a slow one.
const rejoinTimeout = 5 * time.Minute

// rejoinPoll is how often the wait re-reads /nodes.
const rejoinPoll = 5 * time.Second

// Flags are the command's inputs.
type Flags struct {
	Env    string
	Node   string // optional: migrate only this public IP
	Force  bool
	DryRun bool
}

// Run is the `orama node migrate-raft-id` entry point.
func Run(flags *Flags) error {
	if flags.Env == "" {
		return fmt.Errorf("--env is required")
	}
	return execute(flags)
}

// requireStableIDSupport refuses to migrate until every node in the
// environment runs a binary that understands stable raft ids.
//
// The marker file is the probe: a node that has booted on a binary with this
// change records its raft id there, whatever that id currently is. A node with
// no marker either has never restarted on the new binary or is not an index
// node — both are reasons not to start.
func requireStableIDSupport(nodes []inspector.Node) error {
	var missing []string
	for _, n := range nodes {
		marker, err := readMarker(n)
		if err != nil {
			return fmt.Errorf("could not check %s for stable-id support: %w\n"+
				"  Every node must be reachable before any node is migrated", n.Host, err)
		}
		if marker == "" {
			missing = append(missing, n.Host)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("these nodes have not booted on a binary that supports stable raft ids: %s\n"+
			"  Finish the rolling upgrade on every node first. Migrating while one node is on the old\n"+
			"  binary makes it re-add every migrated node as a duplicate voter every five minutes",
			strings.Join(missing, ", "))
	}
	return nil
}

// plan is one node's migration.
type plan struct {
	Node      inspector.Node
	PeerID    string
	OverlayIP string
	CurrentID string

	// Resume marks a node a previous run removed from the configuration but did
	// not finish. Its raft removal and tombstone are already done.
	Resume bool
}

// NeedsMigration reports whether there is still work to do for this node.
func (p plan) NeedsMigration() bool { return p.Resume || p.CurrentID != p.PeerID }

func execute(flags *Flags) error {
	nodes, err := remotessh.LoadEnvNodes(flags.Env)
	if err != nil {
		return err
	}
	cleanup, err := remotessh.PrepareNodeKeys(nodes)
	if err != nil {
		return err
	}
	defer cleanup()

	// Every node in the environment, always. --node narrows what is MIGRATED,
	// never who the removal is driven from: filtering this list left a
	// single-element slice, so PickSurvivor found only the target itself and
	// the documented per-node form could never run.
	allNodes := nodes

	targets := nodes
	if flags.Node != "" {
		targets = remotessh.FilterByIP(nodes, flags.Node)
		if len(targets) == 0 {
			return fmt.Errorf("node %s not found in the %s environment", flags.Node, flags.Env)
		}
	}

	// Every node must be on a binary that understands stable raft ids before
	// ANY node adopts one.
	//
	// An old-binary leader keys orphan recovery on the raft id alone and
	// re-adds by address, so a node that has already migrated is invisible to
	// it and gets a second member every five minutes — the failure this
	// migration exists to prevent, caused by running the migration.
	if err := requireStableIDSupport(allNodes); err != nil {
		return err
	}

	plans, err := buildPlans(allNodes, targets)
	if err != nil {
		return err
	}

	pending := make([]plan, 0, len(plans))
	for _, p := range plans {
		if p.NeedsMigration() {
			pending = append(pending, p)
		}
	}

	fmt.Printf("Raft identity in %s\n\n", flags.Env)
	for _, p := range plans {
		mark := "already stable"
		if p.NeedsMigration() {
			mark = fmt.Sprintf("%s → %s", p.CurrentID, p.PeerID)
		}
		fmt.Printf("  %-16s %s\n", p.Node.Host, mark)
	}
	fmt.Println()

	if len(pending) == 0 {
		fmt.Println("✓ Every node already has a stable raft id. Nothing to do.")
		return nil
	}
	if flags.DryRun {
		fmt.Printf("--dry-run: %d node(s) would be migrated.\n", len(pending))
		return nil
	}

	if !flags.Force {
		fmt.Printf("Each node is removed from raft and rejoins under its peer id, one at a time.\n")
		fmt.Printf("Its local raft state is discarded and replicated back from the leader.\n")
		fmt.Printf("Type 'yes' to confirm: ")
		input, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if strings.TrimSpace(input) != "yes" {
			fmt.Println("Aborted.")
			return nil
		}
		fmt.Println()
	}

	for _, p := range pending {
		if err := migrateOne(allNodes, p); err != nil {
			return fmt.Errorf("migrating %s: %w\n"+
				"  The cluster is intact; re-run to continue from here", p.Node.Host, err)
		}
	}

	fmt.Printf("\n✓ %d node(s) migrated to stable raft ids\n", len(pending))
	return nil
}

// migrateOne moves a single node, refusing anything that would cost quorum.
func migrateOne(all []inspector.Node, p plan) error {
	survivor, err := clusterops.PickSurvivor(all, p.Node.Host)
	if err != nil {
		return err
	}

	fmt.Printf("── %s (%s → %s)\n", p.Node.Host, p.CurrentID, p.PeerID)

	if p.Resume {
		fmt.Printf("   resuming: already removed from raft by an earlier run\n")
		return finishMigration(survivor, p)
	}

	members, err := clusterops.RaftMembers(survivor)
	if err != nil {
		return err
	}
	// The planned-removal rule. A node being migrated is up and answering by
	// definition, and the eviction rule refuses exactly that — using it here
	// meant the migration could not execute a single step on a healthy cluster.
	if reason := rqlite.SafeToRemoveMember(members, p.CurrentID); reason != "" {
		return fmt.Errorf("refusing to remove %s: %s", p.CurrentID, reason)
	}

	// Remove first, then wipe. The other order would leave a node with no raft
	// state still in the configuration — it would rejoin under the OLD id from
	// its unit's arguments and the migration would silently do nothing.
	if err := clusterops.RemoveRaftMember(survivor, p.CurrentID); err != nil {
		return err
	}

	raftAddr := fmt.Sprintf("%s:%d", p.OverlayIP, constants.RQLiteRaftPort)
	if err := clusterops.WriteTombstone(survivor, p.CurrentID, raftAddr, p.PeerID, survivor.Host); err != nil {
		return err
	}
	fmt.Printf("   ✓ tombstoned, so orphan recovery does not re-add the old id\n")

	return finishMigration(survivor, p)
}

// finishMigration performs the half that runs on the target: reset it under the
// new id, wait for it back, and retire the tombstone.
func finishMigration(survivor inspector.Node, p plan) error {
	joinAddr := fmt.Sprintf("%s:%d", survivorOverlayIP(survivor, p), constants.RQLiteHTTPPort)

	if err := resetNodeIdentity(p.Node, p.PeerID, joinAddr); err != nil {
		return fmt.Errorf("the old id was removed but the node could not be reset: %w\n"+
			"  It is out of the cluster until this succeeds", err)
	}
	fmt.Printf("   ✓ raft state cleared and restarted under the new id\n")

	if err := waitForRejoin(survivor, p.PeerID); err != nil {
		return err
	}
	fmt.Printf("   ✓ rejoined as a voter under %s\n", p.PeerID)

	// The tombstone has done its job. Leaving it would keep a spent entry in a
	// table the eviction path reads every tick.
	if err := clusterops.ClearTombstone(survivor, p.CurrentID); err != nil {
		fmt.Printf("   ! could not clear the spent tombstone for %s: %v\n", p.CurrentID, err)
	}
	return nil
}

// waitForRejoin blocks until the node is back in the raft configuration as a
// REACHABLE VOTER under its new id.
//
// Presence alone is not enough, and this is what makes the migration serial in
// any useful sense. A member appears in the configuration the instant the join
// commits, long before it has replicated the log; returning then would mean
// removing the next voter while the previous one is still catching up. On a
// three-voter cluster that is one usefully-caught-up voter out of three.
func waitForRejoin(survivor inspector.Node, nodeID string) error {
	deadline := time.Now().Add(rejoinTimeout)
	lastSeen := "absent from the configuration"

	for {
		members, err := clusterops.RaftMembers(survivor)
		if err != nil {
			lastSeen = fmt.Sprintf("could not read the configuration: %v", err)
		} else {
			lastSeen = "absent from the configuration"
			for _, m := range members {
				if m.ID != nodeID {
					continue
				}
				if m.Voter && m.Reachable {
					return nil
				}
				lastSeen = fmt.Sprintf("present but voter=%v reachable=%v", m.Voter, m.Reachable)
			}
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("%s did not rejoin as a reachable voter within %s (%s) — "+
				"check `orama node logs --env <env> --node <ip>` on it", nodeID, rejoinTimeout, lastSeen)
		}
		time.Sleep(rejoinPoll)
	}
}

// resetNodeIdentity stops the index rqlite on the target, discards its raft
// state, records the new id, and starts it again joining through joinAddr.
//
// Rewriting the unit's env file is the whole point, and leaving it out was a
// silent disaster: -node-id reaches rqlited only through EXTRA_ARGS in
// namespaces/index/rqlite.env, and JOIN_ARGS is only written when the data
// directory has no raft state. Wiping the directory and restarting against the
// OLD env file gives rqlited an empty data dir, no id and no join — so it
// bootstraps a brand-new single-node cluster under its old address, with an
// empty database, and elects itself leader. Nothing on the node notices,
// because a solo cluster answers /status and has a reachable leader.
//
// Discarding the raft state is what makes the rejoin clean: rqlite's guidance
// for a node whose identity changed is to come back as a fresh member rather
// than re-associate from a stale local configuration. The data replicates from
// the leader, which is why this only runs after the quorum check.
//
// The env file written here is temporary in the useful sense: once orama-node
// reconciles, EnsureRQLite regenerates it with -node-id read back from the
// marker and no join, because by then the node has raft state again.
func resetNodeIdentity(node inspector.Node, peerID, joinAddr string) error {
	return remotessh.RunSSHStreaming(node, remotessh.SudoPrefix(node)+"bash -c "+
		clusterops.ShellQuote(resetScript(peerID, joinAddr)))
}

// resetScript renders the remote reset. Separate from the SSH call so its shape
// can be tested: it is the one step of the migration that cannot be undone.
func resetScript(peerID, joinAddr string) string {
	return fmt.Sprintf(`set -euo pipefail
DATA_DIR=%[1]q
ENV_FILE=%[2]q
PEER_ID=%[3]q
JOIN_ADDR=%[4]q

systemctl stop orama-namespace-rqlite@index.service

rm -rf "$DATA_DIR"/raft.db "$DATA_DIR"/rsnapshots "$DATA_DIR"/raft "$DATA_DIR"/peers.json "$DATA_DIR"/db.sqlite*
mkdir -p "$DATA_DIR"
printf '%%s\n' "$PEER_ID" > "$DATA_DIR"/%[5]s

# Rewrite EXTRA_ARGS with the new id and point JOIN_ARGS at a survivor, keeping
# every other line of the env file as it was.
if [ ! -f "$ENV_FILE" ]; then
  echo "no env file at $ENV_FILE; is this an index node?" >&2
  exit 1
fi
EXTRA=$(grep '^EXTRA_ARGS=' "$ENV_FILE" | head -1 | cut -d= -f2- | sed 's/-node-id [^ ]*//g')
{
  grep -v '^EXTRA_ARGS=' "$ENV_FILE" | grep -v '^JOIN_ARGS='
  echo "EXTRA_ARGS=$EXTRA -node-id $PEER_ID"
  echo "JOIN_ARGS=-join $JOIN_ADDR"
} > "$ENV_FILE".tmp
mv "$ENV_FILE".tmp "$ENV_FILE"

chown -R orama:orama "$DATA_DIR" "$ENV_FILE"
systemctl start orama-namespace-rqlite@index.service
`, indexRQLiteDataDir, indexRQLiteEnvFile, peerID, joinAddr, rqlite.RaftIDMarkerName)
}

// survivorOverlayIP resolves the overlay address a migrated node joins through.
func survivorOverlayIP(survivor inspector.Node, p plan) string {
	rec, err := clusterops.ResolveNodeRecord(survivor, survivor.Host)
	if err != nil || rec.InternalIP == "" {
		// Fall back to the node being migrated is NOT acceptable — it is the
		// one node that is definitely not in the cluster right now. Report the
		// survivor's public host so the failure names something real.
		return survivor.Host
	}
	return rec.InternalIP
}

// buildPlans works out, for every target, which raft id it is registered under
// and which one it should be.
//
// The current id comes from the raft configuration rather than from the node
// itself, because the configuration is what a removal has to name. Matching is
// on the raft ADDRESS: that is the one field which identifies the same machine
// whether it is registered under an address-derived id or a peer id.
//
// A target that is absent from the configuration is not necessarily a problem.
// A run interrupted between the removal and the rejoin leaves exactly that, and
// the node's own marker says which half it is in: if the marker already holds
// the peer id, the node is mid-migration and the plan resumes from the reset.
// Refusing the whole run in that state — which is what this used to do — made
// the "safe to re-run" promise false in the one case it exists for.
func buildPlans(all, targets []inspector.Node) ([]plan, error) {
	if len(all) == 0 {
		return nil, fmt.Errorf("no nodes to inspect")
	}

	// Any node can answer for the whole cluster: /nodes is the raft
	// configuration, which every member holds.
	members, err := raftAddressesByID(all)
	if err != nil {
		return nil, err
	}

	plans := make([]plan, 0, len(targets))
	for _, n := range targets {
		survivor, err := clusterops.PickSurvivor(all, n.Host)
		if err != nil {
			return nil, err
		}

		rec, err := clusterops.ResolveNodeRecord(survivor, n.Host)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", n.Host, err)
		}
		if rec.PeerID == "" || rec.InternalIP == "" {
			return nil, fmt.Errorf("%s: the node registry has no peer id or overlay address for it; "+
				"migrate it once it has registered", n.Host)
		}

		p := plan{Node: n, PeerID: rec.PeerID, OverlayIP: rec.InternalIP}

		raftAddr := fmt.Sprintf("%s:%d", rec.InternalIP, constants.RQLiteRaftPort)
		if currentID, ok := members[raftAddr]; ok {
			p.CurrentID = currentID
			plans = append(plans, p)
			continue
		}

		// Absent from the configuration. Ask the node which half of a previous
		// run it is in.
		marker, err := readMarker(n)
		if err != nil {
			return nil, fmt.Errorf("%s: no raft member advertises %s and its raft id could not be read: %w", n.Host, raftAddr, err)
		}
		if marker == rec.PeerID {
			p.CurrentID = rec.PeerID
			p.Resume = true
			plans = append(plans, p)
			continue
		}
		return nil, fmt.Errorf("%s: no raft member advertises %s, and the node still records its raft id as %q; "+
			"it is not part of the cluster, so there is nothing to migrate", n.Host, raftAddr, marker)
	}
	return plans, nil
}

// readMarker reads the raft id a node records for itself. A package-level var
// so the pre-flight check can be tested without SSH.
var readMarker = readRemoteMarker

// errUnreachable is returned by tests standing in for an unreachable node.
var errUnreachable = errors.New("unreachable")

// readRemoteMarker reads the raft id a node records for itself.
func readRemoteMarker(node inspector.Node) (string, error) {
	out, err := remotessh.RunSSHOutput(node, remotessh.SudoPrefix(node)+
		fmt.Sprintf("cat %s/%s 2>/dev/null || true", indexRQLiteDataDir, rqlite.RaftIDMarkerName))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// raftAddressesByID maps each member's raft address to the id it is registered
// under, asking whichever node answers first.
func raftAddressesByID(nodes []inspector.Node) (map[string]string, error) {
	var lastErr error
	for _, n := range nodes {
		members, err := clusterops.RaftMembers(n)
		if err != nil {
			lastErr = err
			continue
		}
		byAddr := make(map[string]string, len(members))
		for _, m := range members {
			if m.Addr != "" {
				byAddr[m.Addr] = m.ID
			}
		}
		return byAddr, nil
	}
	return nil, fmt.Errorf("could not read the raft configuration from any node: %w", lastErr)
}
