package upgrade

import (
	"fmt"
	"os"
)

// Handle executes the upgrade command
// Run executes the upgrade command.
func Run(flags *Flags) error {
	// Remote rolling upgrade when --env is specified
	if flags.Env != "" {
		return NewRemoteUpgrader(flags).Execute()
	}

	// Local upgrade: requires root
	if os.Geteuid() != 0 {
		return fmt.Errorf("production upgrade must be run as root (use sudo)")
	}

	return NewOrchestrator(flags).Execute()
}
