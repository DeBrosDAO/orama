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

	// Check root privileges
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
