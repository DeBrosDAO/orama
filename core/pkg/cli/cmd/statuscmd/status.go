// Package statuscmd provides the top-level `orama status`.
//
// It is the shortest view of the same collection `orama monitor` runs: SSH into
// every node in an environment, run `orama node report --json`, and summarise.
// It used to run that SSH fan-out and parse those reports itself, with its own
// idea of what "healthy" meant, so the two commands could disagree about the
// same cluster at the same moment. Now both read one snapshot.
package statuscmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/DeBrosOfficial/network/pkg/cli"
	"github.com/DeBrosOfficial/network/pkg/cli/monitor"
	"github.com/DeBrosOfficial/network/pkg/cli/monitor/display"
	"github.com/spf13/cobra"
)

var (
	envFlag  string
	jsonFlag bool
)

// collectTimeout bounds each node's report; a node slower than this is
// reported unreachable rather than holding up the whole summary.
const collectTimeout = 30 * time.Second

// Cmd is the top-level "status" command — health summary for the fleet.
var Cmd = &cobra.Command{
	Use:   "status",
	Short: "Show health status of your nodes",
	Long: `Check the health of all your nodes in an environment.

A node is healthy when its gateway answers and its RQLite has settled into
Leader or Follower. For the numbers behind the verdict use 'orama monitor
cluster'; for the state of a single machine you are logged into, 'orama node
status'.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		env := envFlag
		if env == "" {
			active, err := cli.GetActiveEnvironment()
			if err != nil {
				return fmt.Errorf("no --env given and no active environment: %w", err)
			}
			env = active.Name
		}

		snap, err := monitor.CollectOnce(context.Background(), monitor.CollectorConfig{
			Env:     env,
			Timeout: collectTimeout,
		})
		if err != nil {
			return err
		}
		if jsonFlag {
			return display.StatusJSON(snap, os.Stdout)
		}
		return display.StatusTable(snap, os.Stdout)
	},
}

func init() {
	Cmd.Flags().StringVar(&envFlag, "env", "", "Environment (default: active)")
	Cmd.Flags().BoolVar(&jsonFlag, "json", false, "Output as JSON")
}
