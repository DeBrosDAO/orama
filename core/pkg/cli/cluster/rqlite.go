package cluster

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// HandleRQLite handles the "orama cluster rqlite" subcommand group.
func HandleRQLite(args []string) {
	if len(args) == 0 {
		showRQLiteHelp()
		return
	}

	subcommand := args[0]
	subargs := args[1:]

	switch subcommand {
	case "status":
		handleRQLiteStatus()
	case "voters":
		handleRQLiteVoters()
	case "backup":
		handleRQLiteBackup(subargs)
	case "help":
		showRQLiteHelp()
	default:
		fmt.Fprintf(os.Stderr, "Unknown rqlite subcommand: %s\n", subcommand)
		showRQLiteHelp()
		os.Exit(1)
	}
}

// handleRQLiteStatus shows detailed Raft state for the local node.
func handleRQLiteStatus() {
	fmt.Printf("RQLite Raft Status\n")
	fmt.Printf("==================\n\n")

	status, err := queryRQLiteStatus()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Node Configuration\n")
	fmt.Printf("  Node ID:        %s\n", status.Store.NodeID)
	fmt.Printf("  Raft Address:   %s\n", status.Store.Address)
	fmt.Printf("  HTTP Address:   %s\n", status.HTTP.Address)
	fmt.Printf("  Data Directory: %s\n", status.Store.Dir)
	fmt.Println()

	fmt.Printf("Raft State\n")
	fmt.Printf("  State:          %s\n", strings.ToUpper(status.Store.Raft.State))
	fmt.Printf("  Current Term:   %d\n", status.Store.Raft.Term)
	fmt.Printf("  Applied Index:  %d\n", status.Store.Raft.AppliedIndex)
	fmt.Printf("  Commit Index:   %d\n", status.Store.Raft.CommitIndex)
	fmt.Printf("  Leader:         %s\n", status.Store.Raft.Leader)

	if status.Store.Raft.AppliedIndex < status.Store.Raft.CommitIndex {
		lag := status.Store.Raft.CommitIndex - status.Store.Raft.AppliedIndex
		fmt.Printf("  Replication Lag: %d entries behind\n", lag)
	} else {
		fmt.Printf("  Replication Lag: none (fully caught up)\n")
	}

	if status.Node.Uptime != "" {
		fmt.Printf("  Uptime:         %s\n", status.Node.Uptime)
	}
	fmt.Println()
}

// handleRQLiteVoters shows the current voter list from /nodes.
func handleRQLiteVoters() {
	fmt.Printf("RQLite Cluster Voters\n")
	fmt.Printf("=====================\n\n")

	nodes, err := queryRQLiteNodes(true)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	voters := 0
	nonVoters := 0

	fmt.Printf("%-20s %-30s %-8s %-10s %-10s\n",
		"NODE ID", "ADDRESS", "ROLE", "LEADER", "REACHABLE")
	fmt.Printf("%-20s %-30s %-8s %-10s %-10s\n",
		strings.Repeat("-", 20),
		strings.Repeat("-", 30),
		strings.Repeat("-", 8),
		strings.Repeat("-", 10),
		strings.Repeat("-", 10))

	for id, node := range nodes {
		nodeID := id
		if len(nodeID) > 20 {
			nodeID = nodeID[:17] + "..."
		}

		role := "non-voter"
		if node.Voter {
			role = "voter"
			voters++
		} else {
			nonVoters++
		}

		leader := "no"
		if node.Leader {
			leader = "yes"
		}

		reachable := "no"
		if node.Reachable {
			reachable = "yes"
		}

		fmt.Printf("%-20s %-30s %-8s %-10s %-10s\n",
			nodeID, node.Address, role, leader, reachable)
	}

	fmt.Printf("\nTotal: %d voters, %d non-voters\n", voters, nonVoters)
	quorum := (voters / 2) + 1
	fmt.Printf("Quorum requirement: %d/%d voters\n", quorum, voters)
}

// handleRQLiteBackup triggers a manual backup via the RQLite backup endpoint.
func handleRQLiteBackup(args []string) {
	outputFile := getFlagValue(args, "--output")
	if outputFile == "" {
		outputFile = fmt.Sprintf("rqlite-backup-%s.db", time.Now().Format("20060102-150405"))
	}

	fmt.Printf("RQLite Backup\n")
	fmt.Printf("=============\n\n")
	fmt.Printf("Requesting backup from %s/db/backup ...\n", rqliteBaseURL)

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(rqliteBaseURL + "/db/backup")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot connect to RQLite: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "Error: backup request returned HTTP %d: %s\n", resp.StatusCode, string(body))
		os.Exit(1)
	}

	outFile, err := os.Create(outputFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot create output file: %v\n", err)
		os.Exit(1)
	}
	defer outFile.Close()

	written, err := io.Copy(outFile, resp.Body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to write backup: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Backup saved to: %s (%d bytes)\n", outputFile, written)
}

// showRQLiteHelp displays help for rqlite subcommands.
func showRQLiteHelp() {
	fmt.Printf("RQLite Commands\n\n")
	fmt.Printf("Usage: orama cluster rqlite <subcommand> [options]\n\n")
	fmt.Printf("Subcommands:\n")
	fmt.Printf("  status                    - Show detailed Raft state for local node\n")
	fmt.Printf("  voters                    - Show current voter list from cluster\n")
	fmt.Printf("  backup                    - Trigger manual database backup\n")
	fmt.Printf("    Options:\n")
	fmt.Printf("      --output FILE         - Output file path (default: rqlite-backup-<timestamp>.db)\n\n")
	fmt.Printf("Examples:\n")
	fmt.Printf("  orama cluster rqlite status\n")
	fmt.Printf("  orama cluster rqlite voters\n")
	fmt.Printf("  orama cluster rqlite backup --output /tmp/backup.db\n")
}
