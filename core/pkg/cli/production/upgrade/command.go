package upgrade

import (
	"fmt"
	"os"
)

// Handle executes the upgrade command
func Handle(args []string) {
	// Parse flags
	flags, err := ParseFlags(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		os.Exit(1)
	}

	// Remote rolling upgrade when --env is specified
	if flags.Env != "" {
		remote := NewRemoteUpgrader(flags)
		if err := remote.Execute(); err != nil {
			fmt.Fprintf(os.Stderr, "❌ %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Local upgrade: requires root
	if os.Geteuid() != 0 {
		fmt.Fprintf(os.Stderr, "❌ Production upgrade must be run as root (use sudo)\n")
		os.Exit(1)
	}

	// Create orchestrator and execute upgrade
	orchestrator := NewOrchestrator(flags)
	if err := orchestrator.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		os.Exit(1)
	}
}
