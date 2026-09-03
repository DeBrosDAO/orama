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

	// Resolve base domain interactively if not provided (before local/VPS branch)
	if flags.BaseDomain == "" {
		flags.BaseDomain = promptForBaseDomain()
	}

	// Local mode: not running as root → orchestrate install via SSH
	if os.Geteuid() != 0 {
		remote, err := NewRemoteOrchestrator(flags)
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ %v\n", err)
			os.Exit(1)
		}
		if err := remote.Execute(); err != nil {
			fmt.Fprintf(os.Stderr, "❌ %v\n", err)
			os.Exit(1)
		}
		return
	}

	// VPS mode: running as root on the VPS — existing behavior
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
