package cluster

import (
	"fmt"
	"os"
)

// HandleCommand handles cluster subcommands.
func HandleCommand(args []string) {
	if len(args) == 0 {
		ShowHelp()
		return
	}

	subcommand := args[0]
	subargs := args[1:]

	switch subcommand {
	case "status":
		HandleStatus(subargs)
	case "health":
		HandleHealth(subargs)
	case "rqlite":
		HandleRQLite(subargs)
	case "watch":
		HandleWatch(subargs)
	case "help":
		ShowHelp()
	default:
		fmt.Fprintf(os.Stderr, "Unknown cluster subcommand: %s\n", subcommand)
		ShowHelp()
		os.Exit(1)
	}
}

// hasFlag checks if a flag is present in the args slice.
func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

// getFlagValue returns the value of a flag from the args slice.
// Returns empty string if the flag is not found or has no value.
func getFlagValue(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// ShowHelp displays help information for cluster commands.
func ShowHelp() {
	fmt.Printf("Cluster Management Commands\n\n")
	fmt.Printf("Usage: orama cluster <subcommand> [options]\n\n")
	fmt.Printf("Subcommands:\n")
	fmt.Printf("  status                    - Show cluster node status (RQLite + Olric)\n")
	fmt.Printf("    Options:\n")
	fmt.Printf("      --all                 - SSH into all nodes from remote-nodes.conf (TODO)\n")
	fmt.Printf("  health                    - Run cluster health checks\n")
	fmt.Printf("  rqlite <subcommand>       - RQLite-specific commands\n")
	fmt.Printf("    status                  - Show detailed Raft state for local node\n")
	fmt.Printf("    voters                  - Show current voter list\n")
	fmt.Printf("    backup [--output FILE]  - Trigger manual backup\n")
	fmt.Printf("  watch                     - Live cluster status monitor\n")
	fmt.Printf("    Options:\n")
	fmt.Printf("      --interval SECONDS    - Refresh interval (default: 10)\n\n")
	fmt.Printf("Examples:\n")
	fmt.Printf("  orama cluster status\n")
	fmt.Printf("  orama cluster health\n")
	fmt.Printf("  orama cluster rqlite status\n")
	fmt.Printf("  orama cluster rqlite voters\n")
	fmt.Printf("  orama cluster rqlite backup --output /tmp/backup.db\n")
	fmt.Printf("  orama cluster watch --interval 5\n")
}
