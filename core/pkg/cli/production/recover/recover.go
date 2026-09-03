package recover

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/cli/remotessh"
	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/inspector"
)

// Flags holds recover-raft command flags.
type Flags struct {
	Env            string // Target environment
	Leader         string // Leader node IP (highest commit index)
	LeaderRaftAddr string // Explicit leader raft address (host:port); bypasses live resolution
	Force          bool   // Skip confirmation
}

// rqlite on-disk layout (rqlite v8, as deployed by the production installer).
// The committed data lives in db.sqlite* and rsnapshots/, which are SEPARATE
// from the Raft log/stable store (raft.db). This separation is what lets us
// reset the Raft configuration on the leader while preserving all data.
const (
	rqliteRoot     = "/opt/orama/.orama/data/rqlite"
	raftDBFile     = rqliteRoot + "/raft.db"         // Raft log + stable store (BoltDB)
	raftSubdir     = rqliteRoot + "/raft"            // recovery peers.json lives here
	peersFile      = rqliteRoot + "/raft/peers.json" // rqlite reads this iff raft.db is absent
	discoveryPeers = rqliteRoot + "/discovery-peers.json"
	rqliteOwner    = "orama:orama"
	rqlitePort     = constants.RQLiteHTTPPort
)

// Handle is the entry point for the recover-raft command.
func Handle(args []string) {
	flags, err := parseFlags(args)
	if err != nil {
		if err == flag.ErrHelp {
			return
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if err := execute(flags); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func parseFlags(args []string) (*Flags, error) {
	fs := flag.NewFlagSet("recover-raft", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	flags := &Flags{}
	fs.StringVar(&flags.Env, "env", "", "Target environment (devnet, testnet) [required]")
	fs.StringVar(&flags.Leader, "leader", "", "Leader node IP (node with highest commit index) [required]")
	fs.StringVar(&flags.LeaderRaftAddr, "leader-raft-addr", "", "Explicit leader raft address host:port (e.g. 10.0.0.1:10101). Use when quorum is already lost so the leader can't be auto-resolved; bypasses the live-Leader check.")
	fs.BoolVar(&flags.Force, "force", false, "Skip confirmation (DESTRUCTIVE)")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	if flags.Env == "" {
		return nil, fmt.Errorf("--env is required\nUsage: orama node recover-raft --env <devnet|testnet> --leader <ip>")
	}
	if flags.Leader == "" {
		return nil, fmt.Errorf("--leader is required\nUsage: orama node recover-raft --env <devnet|testnet> --leader <ip>")
	}

	return flags, nil
}

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

	// Find leader node
	leaderNodes := remotessh.FilterByIP(nodes, flags.Leader)
	if len(leaderNodes) == 0 {
		return fmt.Errorf("leader %s not found in %s environment", flags.Leader, flags.Env)
	}
	leader := leaderNodes[0]

	// Separate leader from followers
	var followers []inspector.Node
	for _, n := range nodes {
		if n.Host != leader.Host {
			followers = append(followers, n)
		}
	}

	// Resolve the leader's own Raft address (e.g. "10.0.0.1:10101"). This becomes
	// the sole member of the recovery peers.json.
	//   - If --leader-raft-addr is given, trust it (validated). This is the
	//     correct path when quorum is ALREADY lost (the usual recovery case),
	//     since the leader can't report itself as Leader without quorum.
	//   - Otherwise auto-resolve from the still-live cluster, which requires the
	//     named node to currently be the raft Leader.
	var leaderRaftAddr string
	if flags.LeaderRaftAddr != "" {
		if err := validateRaftAddr(flags.LeaderRaftAddr); err != nil {
			return fmt.Errorf("invalid --leader-raft-addr: %w", err)
		}
		leaderRaftAddr = flags.LeaderRaftAddr
		fmt.Printf("Using explicit leader raft address: %s\n", leaderRaftAddr)
	} else {
		leaderRaftAddr, err = resolveLeaderRaftAddr(leader)
		if err != nil {
			return fmt.Errorf("resolve leader raft address (is %s currently the raft leader? if quorum is already lost, pass --leader-raft-addr): %w", leader.Host, err)
		}
	}

	// Print plan
	fmt.Printf("Recover Raft: %s (reforming cluster around %d survivor nodes)\n", flags.Env, len(nodes))
	fmt.Printf("  Leader candidate: %s (%s) — raft addr %s — DATA PRESERVED, config reset to single-node\n", leader.Host, leader.Role, leaderRaftAddr)
	for _, n := range followers {
		fmt.Printf("  - %s (%s) — WIPED and re-joined fresh from leader\n", n.Host, n.Role)
	}
	fmt.Println()

	// Confirm unless --force
	if !flags.Force {
		fmt.Printf("⚠️  THIS WILL:\n")
		fmt.Printf("  1. Stop orama-node on ALL %d survivor nodes (brief main-cluster outage)\n", len(nodes))
		fmt.Printf("  2. On %s: delete raft.db and write a single-node recovery peers.json\n", leader.Host)
		fmt.Printf("     (db.sqlite + rsnapshots preserved — no data loss)\n")
		fmt.Printf("  3. On %d follower(s): WIPE all rqlite state (raft + db.sqlite) so they re-sync fresh\n", len(followers))
		fmt.Printf("  4. Restart leader (single-node), then followers re-join as voters\n")
		fmt.Printf("\nType 'yes' to confirm: ")
		reader := bufio.NewReader(os.Stdin)
		input, _ := reader.ReadString('\n')
		if strings.TrimSpace(input) != "yes" {
			fmt.Println("Aborted.")
			return nil
		}
		fmt.Println()
	}

	// Phase 1: Stop orama-node on ALL nodes
	if err := phase1StopAll(nodes); err != nil {
		return fmt.Errorf("phase 1 (stop all): %w", err)
	}

	// Phase 2: Reset the leader's Raft config to a single-node cluster while
	// preserving its data. rqlite honours peers.json only when raft.db is
	// absent, so we remove raft.db and write the recovery file.
	if err := phase2ResetLeader(leader, leaderRaftAddr); err != nil {
		return fmt.Errorf("phase 2 (reset leader): %w", err)
	}

	// Phase 3: Start the leader and confirm it recovered as Leader WITH its
	// data intact — BEFORE touching any follower. If recovery failed, the
	// followers still hold their copies and we abort without destroying them.
	if err := phase3StartLeader(leader); err != nil {
		return fmt.Errorf("phase 3 (start leader): %w", err)
	}

	// Phase 4: Only now that the leader is proven healthy do we wipe the
	// followers so they re-join fresh from the leader.
	if err := phase4WipeFollowers(followers); err != nil {
		return fmt.Errorf("phase 4 (wipe followers): %w", err)
	}

	// Phase 5: Start remaining nodes serially (each pulls a full snapshot).
	if err := phase5StartFollowers(followers); err != nil {
		return fmt.Errorf("phase 5 (start followers): %w", err)
	}

	// Phase 6: Verify cluster health
	phase6Verify(nodes, leader)

	return nil
}

// resolveLeaderRaftAddr queries the given node's live /nodes endpoint and
// returns the raft address of whichever member reports leader==true. This is
// the node's own WireGuard raft address (e.g. "10.0.0.1:10101").
func resolveLeaderRaftAddr(leader inspector.Node) (string, error) {
	// Cross-check: the node the operator named must ITSELF currently be the raft
	// leader. Otherwise its /nodes view could name a different (partitioned)
	// node, and we'd reset THIS node's raft.db while writing a peers.json whose
	// sole member is someone else — producing a node that isn't in its own
	// cluster config.
	if state := raftState(leader); state != "Leader" {
		return "", fmt.Errorf("node %s reports raft state %q, not Leader — pass --leader as the current leader (highest commit index)", leader.Host, state)
	}

	cmd := fmt.Sprintf("curl -sS --max-time 10 http://localhost:%d/nodes", rqlitePort)
	res := inspector.RunSSH(context.Background(), leader, cmd)
	if !res.OK() {
		return "", fmt.Errorf("query /nodes on %s: %v (stderr: %s)", leader.Host, res.Err, res.Stderr)
	}
	return parseLeaderRaftID([]byte(res.Stdout))
}

// parseLeaderRaftID extracts the raft address of the leader from an rqlite
// /nodes JSON response (a map of nodeID -> node info with a "leader" flag).
func parseLeaderRaftID(nodesJSON []byte) (string, error) {
	var nodes map[string]struct {
		Leader bool `json:"leader"`
	}
	if err := json.Unmarshal(nodesJSON, &nodes); err != nil {
		return "", fmt.Errorf("parse /nodes response: %w", err)
	}
	var leaders []string
	for id, n := range nodes {
		if n.Leader {
			leaders = append(leaders, id)
		}
	}
	if len(leaders) == 0 {
		return "", fmt.Errorf("no node reported leader==true in /nodes response")
	}
	// Map iteration is random; if /nodes somehow reports two leaders (the very
	// split-brain this command recovers from), refuse rather than pick one.
	if len(leaders) > 1 {
		return "", fmt.Errorf("multiple nodes report leader==true (%v) — split-brain; resolve manually before recovery", leaders)
	}
	id := leaders[0]
	// Reject anything malformed so a corrupt/poisoned /nodes response fails fast
	// HERE — before Phase 1 stops the cluster — rather than producing a broken
	// recovery peers.json that a node would then act on.
	if err := validateRaftAddr(id); err != nil {
		return "", fmt.Errorf("leader reported %v in /nodes response", err)
	}
	return id, nil
}

// validateRaftAddr checks that s is a well-formed raft address: a WireGuard
// host:port with an IP host (e.g. "10.0.0.1:10101"). Rejects shell-injection or
// corrupt values before they can reach a recovery peers.json.
func validateRaftAddr(s string) error {
	host, port, err := net.SplitHostPort(s)
	if err != nil || host == "" || port == "" {
		return fmt.Errorf("malformed raft address %q: %v", s, err)
	}
	if net.ParseIP(host) == nil {
		return fmt.Errorf("raft address %q has a non-IP host (expected WireGuard 10.0.0.x)", s)
	}
	return nil
}

// buildSingleNodePeersJSON renders the rqlite recovery peers.json content for a
// single-voter cluster consisting only of the given raft address. The format
// matches what the discovery service writes (id/address/non_voter).
func buildSingleNodePeersJSON(raftAddr string) (string, error) {
	peers := []map[string]interface{}{
		{"id": raftAddr, "address": raftAddr, "non_voter": false},
	}
	data, err := json.MarshalIndent(peers, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal peers.json: %w", err)
	}
	return string(data), nil
}

func phase1StopAll(nodes []inspector.Node) error {
	fmt.Printf("== Phase 1: Stopping orama-node on all %d nodes ==\n", len(nodes))

	var failed []inspector.Node
	for _, node := range nodes {
		sudo := remotessh.SudoPrefix(node)
		fmt.Printf("  Stopping %s ... ", node.Host)

		cmd := fmt.Sprintf("%ssystemctl stop orama-node 2>&1 && echo STOPPED", sudo)
		if err := remotessh.RunSSHStreaming(node, cmd); err != nil {
			fmt.Printf("FAILED\n")
			failed = append(failed, node)
			continue
		}
		fmt.Println()
	}

	// Kill stragglers
	if len(failed) > 0 {
		fmt.Printf("\n⚠️  %d nodes failed to stop. Attempting kill...\n", len(failed))
		for _, node := range failed {
			sudo := remotessh.SudoPrefix(node)
			cmd := fmt.Sprintf("%skillall -9 orama-node rqlited 2>/dev/null; echo KILLED", sudo)
			_ = remotessh.RunSSHStreaming(node, cmd)
		}
	}

	// Enforce quiescence: a lingering rqlited still holds raft.db open and would
	// race the phase-2 `rm -f raft.db` on the leader (data corruption). Poll
	// until every node reports no orama-node/rqlited process, and ABORT if any
	// node can't be quiesced — never proceed to destructive phases otherwise.
	fmt.Printf("\nVerifying all nodes are fully stopped...\n")
	if err := waitAllStopped(nodes, 60*time.Second); err != nil {
		return err
	}
	fmt.Println("  All nodes quiesced.")
	fmt.Println()

	return nil
}

// waitAllStopped polls each node until neither orama-node nor rqlited is running,
// or the timeout elapses. Returns an error naming the nodes that would not stop.
func waitAllStopped(nodes []inspector.Node, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	remaining := make([]inspector.Node, len(nodes))
	copy(remaining, nodes)

	for {
		var stillRunning []inspector.Node
		for _, node := range remaining {
			cmd := `bash -c 'if pgrep -x rqlited >/dev/null 2>&1 || pgrep -x orama-node >/dev/null 2>&1; then echo RUNNING; else echo STOPPED; fi'`
			res := inspector.RunSSH(context.Background(), node, cmd)
			// If we cannot even determine state (SSH failure), treat as still
			// running so we do not proceed to destructive steps on a guess.
			if !res.OK() || strings.TrimSpace(res.Stdout) != "STOPPED" {
				stillRunning = append(stillRunning, node)
			}
		}
		if len(stillRunning) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			hosts := make([]string, len(stillRunning))
			for i, n := range stillRunning {
				hosts[i] = n.Host
			}
			return fmt.Errorf("nodes still running orama-node/rqlited after %s: %v — aborting before destructive phases", timeout, hosts)
		}
		remaining = stillRunning
		time.Sleep(3 * time.Second)
	}
}

func phase2ResetLeader(leader inspector.Node, leaderRaftAddr string) error {
	fmt.Printf("== Phase 2: Resetting leader %s to single-node config (data preserved) ==\n", leader.Host)

	peersJSON, err := buildSingleNodePeersJSON(leaderRaftAddr)
	if err != nil {
		return err
	}
	// base64 the JSON to sidestep all shell-quoting hazards over SSH.
	encoded := base64.StdEncoding.EncodeToString([]byte(peersJSON))

	sudo := remotessh.SudoPrefix(leader)
	script := fmt.Sprintf(`%sbash -c '
set -e
if systemctl is-active --quiet orama-node; then
  echo "ERROR: orama-node still active on leader — aborting"; exit 1
fi
rm -f %s
mkdir -p %s
echo %s | base64 -d > %s
chown -R %s %s
echo "LEADER_RESET_DONE peers=$(cat %s | tr -d "\n")"
'`, sudo, raftDBFile, raftSubdir, encoded, peersFile, rqliteOwner, raftSubdir, peersFile)

	if err := remotessh.RunSSHStreaming(leader, script); err != nil {
		return fmt.Errorf("reset leader %s: %w", leader.Host, err)
	}
	fmt.Println()
	return nil
}

func phase4WipeFollowers(followers []inspector.Node) error {
	fmt.Printf("== Phase 4: Wiping rqlite state on %d follower(s) ==\n", len(followers))

	var failed []string
	for _, node := range followers {
		sudo := remotessh.SudoPrefix(node)
		fmt.Printf("  Wiping %s ... ", node.Host)

		// set -e + active-service guard: never rm live rqlite files, and never
		// leave a follower half-wiped. A failed wipe is FATAL — starting that
		// node later with a stale raft.db would reintroduce the pre-shrink
		// config and cause split-brain.
		script := fmt.Sprintf(`%sbash -c '
set -e
if systemctl is-active --quiet orama-node; then
  echo "ERROR: orama-node still active — refusing to wipe"; exit 1
fi
rm -f %s
rm -rf %s
rm -f %s/db.sqlite %s/db.sqlite-shm %s/db.sqlite-wal
rm -rf %s/rsnapshots
rm -f %s
echo FOLLOWER_WIPE_DONE
'`, sudo, raftDBFile, raftSubdir,
			rqliteRoot, rqliteRoot, rqliteRoot,
			rqliteRoot, discoveryPeers)

		if err := remotessh.RunSSHStreaming(node, script); err != nil {
			fmt.Printf("FAILED: %v\n", err)
			failed = append(failed, node.Host)
			continue
		}
		fmt.Println()
	}
	fmt.Println()

	if len(failed) > 0 {
		return fmt.Errorf("%d follower(s) failed to wipe: %v — do NOT start them (stale raft.db would cause split-brain); investigate and re-run", len(failed), failed)
	}
	return nil
}

func phase3StartLeader(leader inspector.Node) error {
	fmt.Printf("== Phase 3: Starting leader node (%s) ==\n", leader.Host)

	sudo := remotessh.SudoPrefix(leader)
	startCmd := fmt.Sprintf("%ssystemctl start orama-node", sudo)
	if err := remotessh.RunSSHStreaming(leader, startCmd); err != nil {
		return fmt.Errorf("failed to start leader node %s: %w", leader.Host, err)
	}

	fmt.Printf("  Waiting for leader to reach Leader state (up to 120s)...\n")
	deadline := 120
	elapsed := 0
	reachedLeader := false
	for elapsed < deadline {
		time.Sleep(10 * time.Second)
		elapsed += 10

		state := raftState(leader)
		fmt.Printf("  ... %ds: raft state = %q\n", elapsed, state)
		if state == "Leader" {
			reachedLeader = true
			break
		}
	}
	if !reachedLeader {
		return fmt.Errorf("leader %s did not reach Leader state within %ds (check /opt/orama/.orama/logs/rqlite-node.log)", leader.Host, deadline)
	}

	// Data-integrity gate: prove the recovered leader can serve reads and that
	// its schema survived, BEFORE we wipe the followers (their copies are the
	// only fallback if recovery silently lost data).
	tables, err := leaderTableCount(leader)
	if err != nil {
		return fmt.Errorf("leader %s reached Leader but failed the data-integrity check — NOT wiping followers so data is recoverable: %w", leader.Host, err)
	}
	if tables <= 0 {
		return fmt.Errorf("leader %s recovered with an EMPTY schema (%d tables) — aborting before wiping followers to avoid data loss", leader.Host, tables)
	}
	fmt.Printf("  ✅ Leader is up and healthy (%d tables in schema — data preserved).\n\n", tables)
	return nil
}

// leaderTableCount runs a strong-consistency read against the recovered leader
// to confirm the SQLite data survived the raft config reset.
func leaderTableCount(leader inspector.Node) (int, error) {
	cmd := fmt.Sprintf(`curl -sS --max-time 10 -G 'http://localhost:%d/db/query?level=strong' --data-urlencode 'q=SELECT count(*) FROM sqlite_master WHERE type='"'"'table'"'"''`, rqlitePort)
	res := inspector.RunSSH(context.Background(), leader, cmd)
	if !res.OK() {
		return 0, fmt.Errorf("query failed: %v (stderr: %s)", res.Err, res.Stderr)
	}
	// rqlite response: {"results":[{"columns":[...],"values":[[N]]}]}
	var q struct {
		Results []struct {
			Values [][]interface{} `json:"values"`
			Error  string          `json:"error"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &q); err != nil {
		return 0, fmt.Errorf("parse query response %q: %w", res.Stdout, err)
	}
	if len(q.Results) == 0 {
		return 0, fmt.Errorf("empty results in query response: %s", res.Stdout)
	}
	if q.Results[0].Error != "" {
		return 0, fmt.Errorf("rqlite query error: %s", q.Results[0].Error)
	}
	if len(q.Results[0].Values) == 0 || len(q.Results[0].Values[0]) == 0 {
		return 0, fmt.Errorf("no count value in query response: %s", res.Stdout)
	}
	// JSON numbers decode as float64.
	if f, ok := q.Results[0].Values[0][0].(float64); ok {
		return int(f), nil
	}
	return 0, fmt.Errorf("unexpected count type in response: %s", res.Stdout)
}

func phase5StartFollowers(followers []inspector.Node) error {
	fmt.Printf("== Phase 5: Starting %d follower(s) (fresh re-join) ==\n", len(followers))

	var failed []string
	for _, node := range followers {
		sudo := remotessh.SudoPrefix(node)
		fmt.Printf("  Starting %s ... ", node.Host)

		cmd := fmt.Sprintf("%ssystemctl start orama-node && echo STARTED", sudo)
		if err := remotessh.RunSSHStreaming(node, cmd); err != nil {
			fmt.Printf("FAILED: %v\n", err)
			failed = append(failed, node.Host)
			continue
		}
		fmt.Println()

		// Serial start: each follower pulls a full snapshot from the leader.
		// Poll its raft state (health check, not a fixed sleep) before moving on
		// so we don't hammer the leader with concurrent snapshot installs.
		fmt.Printf("  Waiting for %s to join as Follower (up to 180s)...\n", node.Host)
		if joined := waitForState(node, "Follower", 180*time.Second); !joined {
			fmt.Printf("  ⚠️  %s did not report Follower within timeout (may still be syncing a large snapshot)\n", node.Host)
			failed = append(failed, node.Host)
		} else {
			fmt.Printf("  ✅ %s joined.\n", node.Host)
		}
	}

	fmt.Println()
	if len(failed) > 0 {
		return fmt.Errorf("%d follower(s) did not start/join cleanly: %v — check /opt/orama/.orama/logs/rqlite-node.log on each", len(failed), failed)
	}
	return nil
}

// waitForState polls a node's raft state until it matches want, or timeout.
func waitForState(node inspector.Node, want string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if raftState(node) == want {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Second)
	}
}

func phase6Verify(nodes []inspector.Node, leader inspector.Node) {
	fmt.Printf("== Phase 6: Verifying cluster health (up to 180s) ==\n")

	deadline := time.Now().Add(180 * time.Second)
	var states map[string]string
	healthy := false
	for {
		states = make(map[string]string, len(nodes))
		allSettled := true
		leaderSeen := false
		for _, node := range nodes {
			st := raftState(node)
			states[node.Host] = st
			switch st {
			case "Leader":
				leaderSeen = true
			case "Follower":
			default:
				allSettled = false
			}
		}
		if allSettled && leaderSeen {
			healthy = true
			break
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Second)
	}

	fmt.Printf("\n== Cluster status ==\n")
	for _, node := range nodes {
		marker := ""
		if node.Host == leader.Host {
			marker = " ← LEADER"
		}
		fmt.Printf("  %s%s: raft state = %q\n", node.Host, marker, states[node.Host])
	}

	if healthy {
		fmt.Printf("\n✅ Recovery complete — leader elected and all nodes settled.\n\n")
	} else {
		fmt.Printf("\n⚠️  Recovery finished but not all nodes settled (see states above).\n")
		fmt.Printf("   A follower syncing a large snapshot can take longer; re-check shortly.\n\n")
	}
	fmt.Printf("Next steps:\n")
	fmt.Printf("  1. Run 'orama monitor report --env <env>' to verify full cluster health\n")
	fmt.Printf("  2. If a follower still shows an unsettled state, check /opt/orama/.orama/logs/rqlite-node.log\n")
}

// raftState returns the node's current raft state ("Leader"/"Follower"/...) via
// its local /status endpoint, or "" if unreachable.
func raftState(node inspector.Node) string {
	cmd := fmt.Sprintf(`curl -sS --max-time 5 http://localhost:%d/status`, rqlitePort)
	res := inspector.RunSSH(context.Background(), node, cmd)
	if !res.OK() {
		return ""
	}
	var status struct {
		Store struct {
			Raft struct {
				State string `json:"state"`
			} `json:"raft"`
		} `json:"store"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &status); err != nil {
		return ""
	}
	return status.Store.Raft.State
}
