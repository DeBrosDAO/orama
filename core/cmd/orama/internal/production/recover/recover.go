package recover

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/noderesolver"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/clusterops"
	"github.com/DeBrosOfficial/network/pkg/inspector"
	"github.com/DeBrosOfficial/network/pkg/remotessh"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
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
	// raftAddrMarkerFile records the address the node is a member at.
	raftAddrMarkerFile = rqliteRoot + "/" + rqlite.RaftAddrMarkerName
)

// asOramaUser runs a command as the orama user, from the root shell the
// recovery scripts run in. The rqlite data directory is that user's, and a
// root rm, mkdir or > there follows any symlink it has planted; as orama the
// scripts reach only what rqlited already could, and what they create is
// already the orama user's. runuser is util-linux's root-only su, with no PAM
// or sudoers policy to consult.
const asOramaUser = "runuser -u orama --"

// Run is the entry point for the recover-raft command.
func Run(flags *Flags) error {
	if err := flags.validate(); err != nil {
		return err
	}
	return execute(flags)
}

func (f *Flags) validate() error {
	if f.Env == "" {
		return fmt.Errorf("--env is required\nUsage: orama node recover-raft --env <devnet|testnet>")
	}
	// --leader is optional: without it the command reads every node's applied
	// index and keeps the furthest-ahead one, printing what it found so the
	// operator can check before approving.
	return nil
}

func execute(flags *Flags) error {
	nodes, err := noderesolver.ResolveNodes(flags.Env)
	if err != nil {
		return err
	}

	cleanup, err := remotessh.PrepareNodeKeys(nodes)
	if err != nil {
		return err
	}
	defer cleanup()

	// Choose whose data survives.
	//
	// This used to be --leader, required, described as "the node with the
	// highest commit index" and never computed. It decides which copy of the
	// cluster's data is kept: every other node's raft log and database are
	// deleted. Working that out by hand, across six nodes, while quorum is
	// already lost, is how the wrong one gets named.
	leader, err := chooseLeader(nodes, flags.Leader)
	if err != nil {
		return err
	}

	// Separate leader from followers
	var followers []inspector.Node
	for _, n := range nodes {
		if n.Host != leader.Host {
			followers = append(followers, n)
		}
	}

	// Resolve the leader's raft address (e.g. "10.0.0.1:10101") and the raft
	// id it runs under — a libp2p peer id, or its address on a node that
	// predates recorded ids. The two become the sole member of the recovery
	// peers.json, and they are separate: an id written as the address, or the
	// address as the id, leaves the leader outside its own configuration.
	//   - If --leader-raft-addr is given, trust it (validated). This is the
	//     correct path when quorum is ALREADY lost (the usual recovery case),
	//     since the leader can't report itself as Leader without quorum.
	//   - Otherwise auto-resolve from the still-live cluster, which requires the
	//     named node to currently be the raft Leader.
	// Either way the id is the one the leader records for itself (the raft id
	// marker beside its raft state), which is what its rqlited is started with.
	leaderRaft, err := resolveLeaderRaft(leader, flags.LeaderRaftAddr)
	if err != nil {
		return err
	}
	leaderRaftAddr := leaderRaft.addr

	// Print plan
	fmt.Printf("Recover Raft: %s (reforming cluster around %d survivor nodes)\n", flags.Env, len(nodes))
	fmt.Printf("  Leader candidate: %s (%s) — raft id %s at %s — DATA PRESERVED, config reset to single-node\n",
		leader.Host, leader.Role, leaderRaft.id, leaderRaftAddr)
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
		fmt.Printf("  3. On %d follower(s): WIPE all rqlite state (raft + db.sqlite) so they re-sync fresh,\n", len(followers))
		fmt.Printf("     and record the leader as the member they re-join\n")
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
	if err := phase2ResetLeader(leader, leaderRaft); err != nil {
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
	if err := phase4WipeFollowers(followers, leaderRaftAddr); err != nil {
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

// leaderRaft is the recovery leader's raft identity: the id its rqlited runs
// under and the address it listens on.
type leaderRaft struct {
	id, addr string
}

// resolveLeaderRaft decides the leader's raft identity: its address from
// --leader-raft-addr or, without it, from the live cluster; its id from the
// marker the leader records (rqlite.RaftIDMarkerName). A leader with no
// marker was started without -node-id, so its id is its address — rqlite's
// own default. On the live path the id /nodes reports for the leader must be
// that same id.
func resolveLeaderRaft(leader inspector.Node, explicitAddr string) (leaderRaft, error) {
	marker, err := readLeaderMarker(leader)
	if err != nil {
		return leaderRaft{}, fmt.Errorf("read the raft id %s records: %w", leader.Host, err)
	}
	if explicitAddr != "" {
		if err := validateRaftAddr(explicitAddr); err != nil {
			return leaderRaft{}, fmt.Errorf("invalid --leader-raft-addr: %w", err)
		}
		lr := leaderRaft{id: raftIDFor(marker, explicitAddr), addr: explicitAddr}
		fmt.Printf("Using explicit leader raft address: %s (raft id %s)\n", lr.addr, lr.id)
		return lr, nil
	}
	live, err := resolveLiveLeader(leader)
	if err != nil {
		return leaderRaft{}, fmt.Errorf("resolve leader raft address (is %s currently the raft leader? if quorum is already lost, pass --leader-raft-addr): %w", leader.Host, err)
	}
	if want := raftIDFor(marker, live.addr); live.id != want {
		return leaderRaft{}, fmt.Errorf("/nodes on %s names the leader %q, but %s runs as %q; "+
			"resolve which is right before resetting anything", leader.Host, live.id, leader.Host, want)
	}
	return live, nil
}

// raftIDFor is the id a node runs under: its recorded id, else its address.
func raftIDFor(marker, addr string) string {
	if marker != "" {
		return marker
	}
	return addr
}

// readLeaderMarker reads the raft id the leader records for itself; a
// package-level var so the resolution can be tested without SSH.
var readLeaderMarker = func(leader inspector.Node) (string, error) {
	out, err := remotessh.RunSSHOutput(leader, markerReadCommand(remotessh.SudoPrefix(leader)))
	if err != nil {
		return "", fmt.Errorf("read %s on %s: %w", rqlite.RaftIDMarkerName, leader.Host, err)
	}
	return parseMarkerRead(out)
}

// Markers of markerReadCommand's output: the file is absent, or here it is.
const (
	markerAbsent  = "ABSENT"
	markerPresent = "ID:"
)

// markerReadCommand reads the leader's raft id marker as the orama user. A
// missing marker is reported as such; a marker that exists and cannot be read
// fails the command. Folding the two together would take an unreadable marker
// for "started without -node-id" and write the leader into its recovery
// peers.json under its address — an id its rqlited does not run under.
func markerReadCommand(sudo string) string {
	path := rqliteRoot + "/" + rqlite.RaftIDMarkerName
	script := fmt.Sprintf(`if [ ! -e %[1]s ] && [ ! -L %[1]s ]; then printf %[2]s; exit 0; fi; printf %[3]s; cat %[1]s`,
		path, markerAbsent, markerPresent)
	return sudo + asOramaUser + " sh -c " + clusterops.ShellQuote(script)
}

// parseMarkerRead turns markerReadCommand's output into the recorded id, ""
// when there is none.
func parseMarkerRead(out string) (string, error) {
	out = strings.TrimSpace(out)
	if out == markerAbsent {
		return "", nil
	}
	id, ok := strings.CutPrefix(out, markerPresent)
	if !ok {
		return "", fmt.Errorf("unexpected output reading %s: %q", rqlite.RaftIDMarkerName, out)
	}
	id = strings.TrimSpace(id)
	if err := validateRaftID(id); err != nil {
		return "", fmt.Errorf("%s: %w", rqlite.RaftIDMarkerName, err)
	}
	return id, nil
}

// resolveLiveLeader queries the given node's live /nodes endpoint and returns
// the raft id and address of whichever member reports leader==true.
func resolveLiveLeader(leader inspector.Node) (leaderRaft, error) {
	// Cross-check: the node the operator named must ITSELF currently be the raft
	// leader. Otherwise its /nodes view could name a different (partitioned)
	// node, and we'd reset THIS node's raft.db while writing a peers.json whose
	// sole member is someone else — producing a node that isn't in its own
	// cluster config.
	if state := raftState(leader); state != "Leader" {
		return leaderRaft{}, fmt.Errorf("node %s reports raft state %q, not Leader — pass --leader as the current leader (highest commit index)", leader.Host, state)
	}

	cmd := rqlite.NodeShellCurl(remotessh.SudoPrefix(leader), "-sS --max-time 10", "/nodes")
	res := inspector.RunSSH(context.Background(), leader, cmd)
	if !res.OK() {
		return leaderRaft{}, fmt.Errorf("query /nodes on %s: %v (stderr: %s)", leader.Host, res.Err, res.Stderr)
	}
	return parseLeaderRaft([]byte(res.Stdout))
}

// parseLeaderRaft extracts the leader's raft id (the map key) and address
// (its "addr") from an rqlite /nodes response.
func parseLeaderRaft(nodesJSON []byte) (leaderRaft, error) {
	var nodes map[string]struct {
		Addr   string `json:"addr"`
		Leader bool   `json:"leader"`
	}
	if err := json.Unmarshal(nodesJSON, &nodes); err != nil {
		return leaderRaft{}, fmt.Errorf("parse /nodes response: %w", err)
	}
	var leaders []leaderRaft
	for id, n := range nodes {
		if n.Leader {
			leaders = append(leaders, leaderRaft{id: id, addr: n.Addr})
		}
	}
	if len(leaders) == 0 {
		return leaderRaft{}, fmt.Errorf("no node reported leader==true in /nodes response")
	}
	// Map iteration is random; if /nodes somehow reports two leaders (the very
	// split-brain this command recovers from), refuse rather than pick one.
	if len(leaders) > 1 {
		return leaderRaft{}, fmt.Errorf("multiple nodes report leader==true (%v) — split-brain; resolve manually before recovery", leaders)
	}
	lr := leaders[0]
	// Reject anything malformed so a corrupt/poisoned /nodes response fails fast
	// HERE — before Phase 1 stops the cluster — rather than producing a broken
	// recovery peers.json that a node would then act on.
	if err := validateRaftAddr(lr.addr); err != nil {
		return leaderRaft{}, fmt.Errorf("leader reported %v in /nodes response", err)
	}
	if err := validateRaftID(lr.id); err != nil {
		return leaderRaft{}, fmt.Errorf("leader reported %v in /nodes response", err)
	}
	return lr, nil
}

// validateRaftAddr checks that s is a well-formed raft address: a WireGuard
// host:port with an IP host (e.g. "10.0.0.1:10101"). Rejects shell-injection or
// corrupt values before they can reach a recovery peers.json.
func validateRaftAddr(s string) error {
	return rqlite.ValidateRaftAddress(s)
}

// validateRaftID checks that s is a raft id a node runs under (see
// rqlite.ValidateRaftID).
func validateRaftID(s string) error {
	return rqlite.ValidateRaftID(s)
}

// buildSingleNodePeersJSON renders the rqlite recovery peers.json content for a
// single-voter cluster consisting only of the leader, under the id its rqlited
// runs with and at its address. The format matches what the discovery service
// writes (id/address/non_voter).
func buildSingleNodePeersJSON(lr leaderRaft) (string, error) {
	if err := validateRaftAddr(lr.addr); err != nil {
		return "", err
	}
	if err := validateRaftID(lr.id); err != nil {
		return "", err
	}
	peers := []map[string]interface{}{
		{"id": lr.id, "address": lr.addr, "non_voter": false},
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

func phase2ResetLeader(leader inspector.Node, lr leaderRaft) error {
	fmt.Printf("== Phase 2: Resetting leader %s to single-node config (data preserved) ==\n", leader.Host)

	peersJSON, err := buildSingleNodePeersJSON(lr)
	if err != nil {
		return err
	}
	record, err := buildFollowerMembershipRecord(lr.addr, time.Now())
	if err != nil {
		return err
	}
	cmd := remotessh.SudoPrefix(leader) + "bash -c " + clusterops.ShellQuote(leaderResetScript(peersJSON, lr.addr, record))
	if err := remotessh.RunSSHStreaming(leader, cmd); err != nil {
		return fmt.Errorf("reset leader %s: %w", leader.Host, err)
	}
	fmt.Println()
	return nil
}

// leaderResetScript is the root script phase 2 runs on the leader: refuse if
// orama-node is up, then, as the orama user, drop raft.db and write peersJSON
// as the recovery peers.json. It also records addr as the address the leader
// is a member at, and a membership record naming it alone: the configuration
// the recovery installs, so a restart before the membership recorder has run
// does not read a stale address marker as an address change and try to join
// members that are no longer in the cluster (pkg/namespace indexJoinTargets).
// Everything written travels base64-encoded to sidestep every shell-quoting
// hazard; addr is a validated raft address.
func leaderResetScript(peersJSON, addr, record string) string {
	encoded := base64.StdEncoding.EncodeToString([]byte(peersJSON))
	encodedRecord := base64.StdEncoding.EncodeToString([]byte(record))
	recordPath := rqlite.ClusterMembershipPath(rqliteRoot)
	asOrama := fmt.Sprintf(`set -e
rm -f %[1]s
mkdir -p %[2]s
printf %%s %[3]s | base64 -d > %[4]s
echo %[5]s > %[6]s.tmp
mv %[6]s.tmp %[6]s
printf %%s %[7]s | base64 -d > %[8]s.tmp
mv %[8]s.tmp %[8]s
echo "LEADER_RESET_DONE peers=$(tr -d "\n" < %[4]s)"
`, raftDBFile, raftSubdir, encoded, peersFile, addr, raftAddrMarkerFile, encodedRecord, recordPath)
	return fmt.Sprintf(`set -e
if systemctl is-active --quiet orama-node; then
  echo "ERROR: orama-node still active on leader — aborting"; exit 1
fi
%s sh -c %s
`, asOramaUser, clusterops.ShellQuote(asOrama))
}

// buildFollowerMembershipRecord is the membership record a wiped follower is
// left with: it names the leader, so the follower joins it. A follower without
// a join address in node.yaml — the genesis node — would otherwise either
// bootstrap an empty cluster of its own or, holding a record that names no
// other member, refuse to start (see pkg/namespace indexJoinTargets).
func buildFollowerMembershipRecord(leaderRaftAddr string, now time.Time) (string, error) {
	if err := validateRaftAddr(leaderRaftAddr); err != nil {
		return "", err
	}
	data, err := json.Marshal(rqlite.ClusterMembership{FirstSeen: now.UTC(), Members: []string{leaderRaftAddr}})
	if err != nil {
		return "", fmt.Errorf("encode the followers' membership record: %w", err)
	}
	return string(data), nil
}

func phase4WipeFollowers(followers []inspector.Node, leaderRaftAddr string) error {
	fmt.Printf("== Phase 4: Wiping rqlite state on %d follower(s) ==\n", len(followers))

	record, err := buildFollowerMembershipRecord(leaderRaftAddr, time.Now())
	if err != nil {
		return err
	}
	encodedRecord := base64.StdEncoding.EncodeToString([]byte(record))
	recordPath := rqlite.ClusterMembershipPath(rqliteRoot)

	var failed []string
	for _, node := range followers {
		fmt.Printf("  Wiping %s ... ", node.Host)

		// set -e + active-service guard: never rm live rqlite files, and never
		// leave a follower half-wiped. A failed wipe is FATAL — starting that
		// node later with a stale raft.db would reintroduce the pre-shrink
		// config and cause split-brain.
		script := remotessh.SudoPrefix(node) + "bash -c " + clusterops.ShellQuote(followerWipeScript(encodedRecord, recordPath))
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

// followerWipeScript is the root script phase 4 runs on each follower:
// refuse if orama-node is up, then, as the orama user, delete its raft state
// and data so it rejoins from the leader, and leave it the membership record
// (base64 encodedRecord) at recordPath naming the leader.
func followerWipeScript(encodedRecord, recordPath string) string {
	asOrama := fmt.Sprintf(`set -e
rm -f %[1]s
rm -rf %[2]s
rm -f %[3]s/db.sqlite %[3]s/db.sqlite-shm %[3]s/db.sqlite-wal
rm -rf %[3]s/rsnapshots
rm -f %[4]s
printf %%s %[5]s | base64 -d > %[6]s.tmp
mv %[6]s.tmp %[6]s
`, raftDBFile, raftSubdir, rqliteRoot, discoveryPeers, encodedRecord, recordPath)
	return fmt.Sprintf(`set -e
if systemctl is-active --quiet orama-node; then
  echo "ERROR: orama-node still active — refusing to wipe"; exit 1
fi
%s sh -c %s
echo FOLLOWER_WIPE_DONE
`, asOramaUser, clusterops.ShellQuote(asOrama))
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
	cmd := rqlite.NodeShellCurl(remotessh.SudoPrefix(leader),
		`-sS --max-time 10 -G --data-urlencode 'q=SELECT count(*) FROM sqlite_master WHERE type='"'"'table'"'"''`,
		"/db/query?level=strong")
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
	cmd := rqlite.NodeShellCurl(remotessh.SudoPrefix(node), "-sS --max-time 5", "/status")
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

// chooseLeader resolves the node whose data a recovery keeps.
//
// An explicit --leader wins and is only checked for membership: an operator who
// names one has a reason, and this command exists for situations the automatic
// answer may not cover.
func chooseLeader(nodes []inspector.Node, explicit string) (inspector.Node, error) {
	if explicit != "" {
		named := remotessh.FilterByIP(nodes, explicit)
		if len(named) == 0 {
			return inspector.Node{}, fmt.Errorf("--leader %s is not a node in this environment", explicit)
		}
		return named[0], nil
	}

	fmt.Printf("Reading the applied index of %d node(s)...\n\n", len(nodes))
	picked, indexes, err := PickLeader(nodes)
	if err != nil {
		return inspector.Node{}, err
	}
	fmt.Print(FormatIndexes(indexes, picked))
	fmt.Println()
	return picked, nil
}
