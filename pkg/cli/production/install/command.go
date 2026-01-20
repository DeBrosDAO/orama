package install

import (
	"fmt"
	"os"
)

// Handle executes the install command
func Handle(args []string) {
	// Parse flags
	flags, err := ParseFlags(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		os.Exit(1)
	}

	// Create orchestrator
	orchestrator, err := NewOrchestrator(flags)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		os.Exit(1)
	}

	// Validate flags
	if err := orchestrator.validator.ValidateFlags(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Error: %v\n", err)
		os.Exit(1)
	}

	// Check root privileges
	if err := orchestrator.validator.ValidateRootPrivileges(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		os.Exit(1)
	}

	// Check port availability before proceeding
	if err := orchestrator.validator.ValidatePorts(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		os.Exit(1)
	}

	// Execute installation
	if err := orchestrator.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		os.Exit(1)
	}
}
