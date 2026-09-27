// Package statuscmd provides the top-level `orama status`.
//
// It is the shortest view of the same snapshot `orama monitor` reads: from the
// gateway's operator telemetry API by default, or over SSH with --ssh. It used
// to run its own SSH fan-out with its own idea of what "healthy" meant, so the
// two commands could disagree about the same cluster at the same moment. Now
// both read one snapshot.
package statuscmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	cli "github.com/DeBrosOfficial/network/cmd/orama/internal"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/monitor"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/monitor/display"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/printer"
)

// --json is a persistent flag on the root, so it is not defined here.
var (
	envFlag string
	sshFlag bool
)

// Cmd is the top-level "status" command — health summary for the fleet.
var Cmd = &cobra.Command{
	Use:   "status",
	Short: "Show health status of your nodes",
	Long: `Check the health of all your nodes in an environment.

A node is healthy when its gateway answers and its RQLite has settled into
Leader or Follower. The data comes from the gateway's operator telemetry API;
--ssh reads every node over SSH instead, for when no gateway answers. For the
numbers behind the verdict use 'orama monitor cluster'; for the state of a
single machine you are logged into, 'orama node status'.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		env := envFlag
		if env == "" {
			active, err := cli.GetActiveEnvironment()
			if err != nil {
				return fmt.Errorf("no --env given and no active environment: %w", err)
			}
			env = active.Name
		}

		src, err := monitor.NewSource(monitor.Options{Env: env, SSH: sshFlag, SSHTimeout: monitor.DefaultSSHTimeout})
		if err != nil {
			return err
		}
		snap, err := src.Snapshot(cmd.Context())
		if err != nil {
			return err
		}
		if printer.For(cmd).JSONMode() {
			return display.StatusJSON(snap, os.Stdout)
		}
		return display.StatusTable(snap, os.Stdout)
	},
}

func init() {
	Cmd.Flags().StringVar(&envFlag, "env", "", "Environment (default: active)")
	Cmd.Flags().BoolVar(&sshFlag, "ssh", false, "Collect over SSH from every node instead of the gateway API (break-glass)")
}
