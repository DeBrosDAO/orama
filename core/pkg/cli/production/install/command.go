package install

import (
	"os"
)

// Run executes the install command.
func Run(flags *Flags) error {
	// Resolve base domain interactively if not provided (before local/VPS branch)
	if flags.BaseDomain == "" {
		flags.BaseDomain = promptForBaseDomain()
	}

	// Local mode: not running as root → orchestrate install via SSH
	if os.Geteuid() != 0 {
		remote, err := NewRemoteOrchestrator(flags)
		if err != nil {
			return err
		}
		return remote.Execute()
	}

	// VPS mode: running as root on the VPS — existing behavior
	orchestrator, err := NewOrchestrator(flags)
	if err != nil {
		return err
	}
	return orchestrator.Execute()
}
