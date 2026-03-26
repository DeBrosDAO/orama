package cluster

import (
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// HandleWatch handles the "orama cluster watch" command.
// It polls RQLite status and nodes at a configurable interval and reprints a summary.
func HandleWatch(args []string) {
	interval := 10 * time.Second

	// Parse --interval flag
	intervalStr := getFlagValue(args, "--interval")
	if intervalStr != "" {
		secs, err := strconv.Atoi(intervalStr)
		if err != nil || secs < 1 {
			fmt.Fprintf(os.Stderr, "Error: --interval must be a positive integer (seconds)\n")
			os.Exit(1)
		}
		interval = time.Duration(secs) * time.Second
	}

	// Set up signal handling for clean exit
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	fmt.Printf("Watching cluster status (interval: %s, Ctrl+C to exit)\n\n", interval)

	// Initial render
	renderWatchScreen()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			renderWatchScreen()
		case <-sigCh:
			fmt.Printf("\nWatch stopped.\n")
			return
		}
	}
}

// renderWatchScreen clears the terminal and prints a summary of cluster state.
func renderWatchScreen() {
	// Clear screen using ANSI escape codes
	fmt.Print("\033[2J\033[H")

	now := time.Now().Format("2006-01-02 15:04:05")
	fmt.Printf("Cluster Watch  [%s]\n", now)
	fmt.Printf("=======================================\n\n")

	// Query RQLite status
	status, err := queryRQLiteStatus()
	if err != nil {
		fmt.Printf("RQLite: UNREACHABLE (%v)\n\n", err)
	} else {
		fmt.Printf("Local Node: %s\n", status.Store.NodeID)
		fmt.Printf("  State: %-10s  Term: %-6d  Applied: %-8d  Commit: %-8d\n",
			strings.ToUpper(status.Store.Raft.State),
			status.Store.Raft.Term,
			status.Store.Raft.AppliedIndex,
			status.Store.Raft.CommitIndex)
		fmt.Printf("  Leader: %s\n", status.Store.Raft.Leader)
		if status.Node.Uptime != "" {
			fmt.Printf("  Uptime: %s\n", status.Node.Uptime)
		}
		fmt.Println()
	}

	// Query nodes
	nodes, err := queryRQLiteNodes(true)
	if err != nil {
		fmt.Printf("Nodes: UNAVAILABLE (%v)\n\n", err)
	} else {
		total := len(nodes)
		voters := 0
		reachable := 0
		for _, n := range nodes {
			if n.Voter {
				voters++
			}
			if n.Reachable {
				reachable++
			}
		}

		fmt.Printf("Cluster: %d nodes (%d voters), %d/%d reachable\n\n",
			total, voters, reachable, total)

		// Compact table
		fmt.Printf("%-18s %-28s %-7s %-7s %-7s\n",
			"ID", "ADDRESS", "VOTER", "LEADER", "UP")
		fmt.Printf("%-18s %-28s %-7s %-7s %-7s\n",
			strings.Repeat("-", 18),
			strings.Repeat("-", 28),
			strings.Repeat("-", 7),
			strings.Repeat("-", 7),
			strings.Repeat("-", 7))

		for id, node := range nodes {
			nodeID := id
			if len(nodeID) > 18 {
				nodeID = nodeID[:15] + "..."
			}

			voter := " "
			if node.Voter {
				voter = "yes"
			}

			leader := " "
			if node.Leader {
				leader = "yes"
			}

			up := "no"
			if node.Reachable {
				up = "yes"
			}

			fmt.Printf("%-18s %-28s %-7s %-7s %-7s\n",
				nodeID, node.Address, voter, leader, up)
		}
	}

	fmt.Printf("\nPress Ctrl+C to exit\n")
}
