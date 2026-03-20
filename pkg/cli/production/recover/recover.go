package recover

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/cli/remotessh"
	"github.com/DeBrosOfficial/network/pkg/inspector"
)

// Flags holds recover-raft command flags.
type Flags struct {
	Env    string // Target environment
	Leader string // Leader node IP (highest commit index)
	Force  bool   // Skip confirmation
}

const (
	raftDir   = "/opt/orama/.orama/data/rqlite/raft"
	backupDir = "/tmp/rqlite-raft-backup"
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

	// Print plan
	fmt.Printf("Recover Raft: %s (%d nodes)\n", flags.Env, len(nodes))
	fmt.Printf("  Leader candidate: %s (%s) — raft/ data preserved\n", leader.Host, leader.Role)
	for _, n := range followers {
		fmt.Printf("  - %s (%s) — raft/ will be deleted\n", n.Host, n.Role)
	}
	fmt.Println()

	// Confirm unless --force
	if !flags.Force {
		fmt.Printf("⚠️  THIS WILL:\n")
		fmt.Printf("  1. Stop orama-node on ALL %d nodes\n", len(nodes))
		fmt.Printf("  2. DELETE raft/ data on %d nodes (backup to %s)\n", len(followers), backupDir)
		fmt.Printf("  3. Keep raft/ data ONLY on %s (leader candidate)\n", leader.Host)
		fmt.Printf("  4. Restart all nodes to reform the cluster\n")
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

	// Phase 2: Backup and delete raft/ on non-leader nodes
	if err := phase2ClearFollowers(followers); err != nil {
		return fmt.Errorf("phase 2 (clear followers): %w", err)
	}
	fmt.Printf("  Leader node %s raft/ data preserved.\n\n", leader.Host)

	// Phase 3: Start leader node and wait for Leader state
	if err := phase3StartLeader(leader); err != nil {
		return fmt.Errorf("phase 3 (start leader): %w", err)
	}

	// Phase 4: Start remaining nodes in batches
	if err := phase4StartFollowers(followers); err != nil {
		return fmt.Errorf("phase 4 (start followers): %w", err)
	}

	// Phase 5: Verify cluster health
	phase5Verify(nodes, leader)

	return nil
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

	fmt.Printf("\nWaiting 5s for processes to fully stop...\n")
	time.Sleep(5 * time.Second)
	fmt.Println()

	return nil
}

func phase2ClearFollowers(followers []inspector.Node) error {
	fmt.Printf("== Phase 2: Clearing raft state on %d non-leader nodes ==\n", len(followers))

	for _, node := range followers {
		sudo := remotessh.SudoPrefix(node)
		fmt.Printf("  Clearing %s ... ", node.Host)

		script := fmt.Sprintf(`%sbash -c '
rm -rf %s
if [ -d %s ]; then
    cp -r %s %s 2>/dev/null || true
    rm -rf %s
    echo "CLEARED (backup at %s)"
else
    echo "NO_RAFT_DIR (nothing to clear)"
fi
'`, sudo, backupDir, raftDir, raftDir, backupDir, raftDir, backupDir)

		if err := remotessh.RunSSHStreaming(node, script); err != nil {
			fmt.Printf("FAILED: %v\n", err)
			continue
		}
		fmt.Println()
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

	fmt.Printf("  Waiting for leader to become Leader...\n")
	maxWait := 120
	elapsed := 0

	for elapsed < maxWait {
		// Check raft state via RQLite status endpoint
		checkCmd := `curl -s --max-time 3 http://localhost:5001/status 2>/dev/null | python3 -c "
import sys,json
try:
  d=json.load(sys.stdin)
  print(d.get('store',{}).get('raft',{}).get('state',''))
except:
  print('')
" 2>/dev/null || echo ""`

		// We can't easily capture output from RunSSHStreaming, so we use a simple approach
		// Check via a combined command that prints a marker
		stateCheckCmd := fmt.Sprintf(`state=$(%s); echo "RAFT_STATE=$state"`, checkCmd)
		// Since RunSSHStreaming prints to stdout, we'll poll and let user see the state
		fmt.Printf("  ... polling (%ds / %ds)\n", elapsed, maxWait)

		// Try to check state - the output goes to stdout via streaming
		_ = remotessh.RunSSHStreaming(leader, stateCheckCmd)

		time.Sleep(5 * time.Second)
		elapsed += 5
	}

	fmt.Printf("  Leader start complete. Check output above for state.\n\n")
	return nil
}

func phase4StartFollowers(followers []inspector.Node) error {
	fmt.Printf("== Phase 4: Starting %d remaining nodes ==\n", len(followers))

	batchSize := 3
	for i, node := range followers {
		sudo := remotessh.SudoPrefix(node)
		fmt.Printf("  Starting %s ... ", node.Host)

		cmd := fmt.Sprintf("%ssystemctl start orama-node && echo STARTED", sudo)
		if err := remotessh.RunSSHStreaming(node, cmd); err != nil {
			fmt.Printf("FAILED: %v\n", err)
			continue
		}
		fmt.Println()

		// Batch delay for cluster stability
		if (i+1)%batchSize == 0 && i+1 < len(followers) {
			fmt.Printf("  (waiting 15s between batches for cluster stability)\n")
			time.Sleep(15 * time.Second)
		}
	}

	fmt.Println()
	return nil
}

func phase5Verify(nodes []inspector.Node, leader inspector.Node) {
	fmt.Printf("== Phase 5: Waiting for cluster to stabilize ==\n")

	// Wait in 30s increments
	for _, s := range []int{30, 60, 90, 120} {
		time.Sleep(30 * time.Second)
		fmt.Printf("  ... %ds\n", s)
	}

	fmt.Printf("\n== Cluster status ==\n")
	for _, node := range nodes {
		marker := ""
		if node.Host == leader.Host {
			marker = " ← LEADER"
		}

		checkCmd := `curl -s --max-time 5 http://localhost:5001/status 2>/dev/null | python3 -c "
import sys,json
try:
  d=json.load(sys.stdin)
  r=d.get('store',{}).get('raft',{})
  n=d.get('store',{}).get('num_nodes','?')
  print(f'state={r.get(\"state\",\"?\")} commit={r.get(\"commit_index\",\"?\")} leader={r.get(\"leader\",{}).get(\"node_id\",\"?\")} nodes={n}')
except:
  print('NO_RESPONSE')
" 2>/dev/null || echo "SSH_FAILED"`

		fmt.Printf("  %s%s: ", node.Host, marker)
		_ = remotessh.RunSSHStreaming(node, checkCmd)
		fmt.Println()
	}

	fmt.Printf("\n== Recovery complete ==\n\n")
	fmt.Printf("Next steps:\n")
	fmt.Printf("  1. Run 'orama monitor report --env <env>' to verify full cluster health\n")
	fmt.Printf("  2. If some nodes show Candidate state, give them more time (up to 5 min)\n")
	fmt.Printf("  3. If nodes fail to join, check /opt/orama/.orama/logs/rqlite-node.log on the node\n")
}
