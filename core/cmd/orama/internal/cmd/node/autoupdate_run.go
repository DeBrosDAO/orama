package node

import (
	"github.com/DeBrosOfficial/network/cmd/orama/internal/cmdmeta"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/updateagent"
	"github.com/spf13/cobra"
)

var autoupdateRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Look for a newer release on the cluster's channel and act on it (requires sudo)",
	Long: `Run this node's auto-update agent once. orama-autoupdate.timer runs it every
15 minutes on every node; running it by hand does the same thing.

The agent reads the cluster's policy (orama maint cluster settings show): auto-update
off, notify or auto; the channel; the maintenance window; the release repository.
It does nothing unless the cluster stored a release repository and this node
adopted a release root (orama node trust add-root).

It fetches the channel's metadata and verifies it against the adopted root:
every role at its threshold, an unexpired timestamp, a snapshot no older than the
newest this node has accepted, and the channel's own keys for the channel's own
paths. What does not verify is refused, reported in 'orama monitor', and never
installed.

With notify (the default) a newer release is reported in 'orama monitor' and
nothing is installed. With auto the node installs it only when

  - the cluster is not degraded and a majority of the raft voters are up;
  - the hour is inside the maintenance window, if there is one;
  - no node has failed the release (a failure anywhere marks the release bad for
    every node, until a newer release supersedes it);
  - it is this node's turn in the rollout plan: followers first, the leader
    last, nameservers spaced, one node at a time;
  - it holds the cluster-wide rollout lock.

The install is 'orama maint node stage-archive --release-only', keeping the release it
replaces, then 'orama node upgrade --restart', then the health gate. If the
upgrade or the gate fails, the previous release is put back and the node is
upgraded onto it again; the release is then marked bad for the cluster. A
validator (a machine that runs the chain) is never installed automatically.

If a run is killed in the middle of an install, the next run finishes it first
(the intent is in /var/lib/orama-autoupdate/install-intent.json), whatever the
policy now says. One agent runs at a time on a machine.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return updateagent.Run(cmd.Context(), cmd.OutOrStdout())
	},
}

func init() {
	autoupdateCmd.AddCommand(cmdmeta.MarkNodeLocal(autoupdateRunCmd))

	// orama-autoupdate.service on every installed node runs `orama node
	// autoupdate run`. The command moved to `orama maint node autoupdate run`;
	// this keeps the path the installed unit uses until an upgrade rewrites it.
	unitPath := &cobra.Command{
		Use:    autoupdateCmd.Use,
		Short:  "Kept for installed units: orama maint node autoupdate",
		Hidden: true,
	}
	unitPath.AddCommand(cmdmeta.HiddenAlias(autoupdateRunCmd))
	Cmd.AddCommand(unitPath)
}
