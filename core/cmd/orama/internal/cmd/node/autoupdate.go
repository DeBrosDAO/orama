package node

import (
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/autoupdate"
	"github.com/DeBrosOfficial/network/pkg/releaseverify"
	"github.com/spf13/cobra"
)

// The check does not install anything. Mode auto still only prints upgrade:
// installing is autoupdate.Upgrade, which a caller runs after this decision
// while it holds the rollout lock, one node at a time.
var autoupdateCmd = &cobra.Command{
	Use:   "autoupdate",
	Short: "Decide whether a newer release should be installed",
	Long: `Report what this cluster should do with a candidate release.

The default mode is notify: a newer verified release is reported and not
installed. auto means the node may install, and only when the cluster is
healthy, the release is newer, and the maintenance window is open. The
install itself is one node at a time and is not performed by this command.

A release that fails TUF verification, including a rolled-back snapshot or
an expired timestamp, is refused. So is a downgrade and a release a previous
health-gate failure marked bad.

A validator (--role validator) is never auto: the mode is refused, and chain
upgrades are staged explicitly with 'orama global stage-oramad'.`,
	Args: cobra.NoArgs,
	RunE: runAutoupdate,
}

var (
	auMode      string
	auChannel   string
	auCurrent   string
	auCandidate string
	auDegraded  bool
	auVoters    int
	auHealthy   int
	auBad       bool
	auVerify    string
	auWindow    string
	auRole      string
)

func init() {
	autoupdateCmd.Flags().StringVar(&auMode, "mode", autoupdate.ModeNotify, "off, notify, or auto")
	autoupdateCmd.Flags().StringVar(&auChannel, "channel", "stable", "release channel")
	autoupdateCmd.Flags().StringVar(&auCurrent, "current", "", "version installed now")
	autoupdateCmd.Flags().StringVar(&auCandidate, "candidate", "", "version being considered")
	autoupdateCmd.Flags().BoolVar(&auDegraded, "degraded", false, "cluster is already degraded")
	autoupdateCmd.Flags().IntVar(&auVoters, "voters", 3, "raft voters")
	autoupdateCmd.Flags().IntVar(&auHealthy, "healthy-voters", 2, "raft voters that are up")
	autoupdateCmd.Flags().BoolVar(&auBad, "bad", false, "candidate was marked bad by a failed health gate")
	autoupdateCmd.Flags().StringVar(&auVerify, "verify", "", "simulated TUF failure: rollback, freeze, threshold, or hash")
	autoupdateCmd.Flags().StringVar(&auRole, "role", autoupdate.RoleCluster, "this node's role: cluster or validator")
	autoupdateCmd.Flags().StringVar(&auWindow, "window", "", "maintenance window as start-end hours, for example 1-5")
	Cmd.AddCommand(autoupdateCmd)
}

func runAutoupdate(cmd *cobra.Command, _ []string) error {
	if auCurrent == "" || auCandidate == "" {
		return clierr.Usage("autoupdate needs --current and --candidate")
	}
	settings := autoupdate.DefaultSettings()
	settings.Mode = auMode
	settings.Channel = auChannel
	settings.Role = auRole
	if auWindow != "" {
		var start, end int
		if _, err := fmt.Sscanf(auWindow, "%d-%d", &start, &end); err != nil {
			return clierr.Usage("--window must be start-end hours, for example 1-5")
		}
		settings.WindowStart = start
		settings.WindowEnd = end
	}
	var verifyErr error
	switch auVerify {
	case "":
	case "rollback":
		verifyErr = releaseverify.ErrRollback
	case "freeze":
		verifyErr = releaseverify.ErrFreeze
	case "threshold":
		verifyErr = releaseverify.ErrThreshold
	case "hash":
		verifyErr = releaseverify.ErrTargetHash
	default:
		return clierr.Usage("--verify must be rollback, freeze, threshold, or hash")
	}
	decision, err := autoupdate.Decide(settings, autoupdate.Health{
		Degraded:      auDegraded,
		Voters:        auVoters,
		HealthyVoters: auHealthy,
	}, time.Now(), auCurrent, autoupdate.Candidate{
		Version: auCandidate,
		Channel: auChannel,
		Bad:     auBad,
	}, verifyErr)
	if err != nil {
		return clierr.Usage("%s", err.Error())
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", decision.Action, decision.Reason)
	return nil
}
