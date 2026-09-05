// Package rolloutcmd defines the rollout command.
//
// The same command is mounted as `orama rollout` and as `orama node rollout`.
// They used to be separate: one pushed and restarted without building, the
// other built first, and only one of them attempted to leave the raft leader
// until last. Which behaviour an operator got depended on which of two
// identically named commands they happened to type.
package rolloutcmd

import (
	"github.com/DeBrosOfficial/network/pkg/cli/production/rollout"
	rolloutplan "github.com/DeBrosOfficial/network/pkg/rollout"
	"github.com/spf13/cobra"
)

// Cmd is the top-level "rollout" command.
var Cmd = NewCmd("rollout")

// NewCmd builds the rollout command under the given name. Each call returns a
// command with its own flag storage; cobra commands cannot be shared between
// parents.
func NewCmd(use string) *cobra.Command {
	var flags rollout.Flags

	cmd := &cobra.Command{
		Use:   use,
		Short: "Build, push, and rolling upgrade every node in an environment",
		Long: `Full deployment pipeline: build the binary archive, push it to every node,
then upgrade them one at a time.

The rolling upgrade prints its plan — which node holds the raft leadership and
the order the restarts happen in — and stops unless --yes is given.

'orama rollout' and 'orama node rollout' are the same command.

Examples:
  orama rollout --env testnet             # Build, push, then print the plan
  orama rollout --env testnet --yes       # Execute the plan
  orama rollout --env testnet --no-build  # Reuse the existing archive`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return rollout.Run(&flags)
		},
	}

	f := cmd.Flags()
	f.StringVar(&flags.Env, "env", "", "Target environment (devnet, testnet) [required]")
	f.BoolVar(&flags.NoBuild, "no-build", false, "Skip the build step and reuse the existing archive")
	f.BoolVar(&flags.Yes, "yes", false, "Execute the rollout plan instead of only printing it")
	f.IntVar(&flags.Delay, "delay", int(rolloutplan.GateBudget.Seconds()),
		"Seconds a node has to rejoin the cluster after its upgrade before the rollout stops")

	return cmd
}
