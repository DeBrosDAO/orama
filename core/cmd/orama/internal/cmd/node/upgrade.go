package node

import (
	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/upgrade"
	"github.com/DeBrosOfficial/network/pkg/rollout"
	"github.com/spf13/cobra"
)

var upgradeFlags upgrade.Flags

// upgradeAnyoneClient is set only when --anyone-client reaches the new binary
// through the post-swap re-exec; see upgradeReexecAnyoneClientFlag.
var upgradeAnyoneClient bool

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Upgrade existing installation (requires sudo)",
	Long: `Upgrade the Orama node binary and optionally restart services.
Uses rolling restart with quorum safety to ensure zero downtime.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := checkUpgradeAnyoneClient(upgradeAnyoneClient, upgradeFlags.ReexecedAfterBinarySwap); err != nil {
			return err
		}
		// Nameserver is a pointer so the orchestrator can tell "not given"
		// (keep the saved preference) from an explicit --nameserver=false.
		if cmd.Flags().Changed("nameserver") {
			v, err := cmd.Flags().GetBool("nameserver")
			if err != nil {
				return err
			}
			upgradeFlags.Nameserver = &v
		}
		return upgrade.Run(&upgradeFlags)
	},
}

func init() {
	upgradeCmd.SetFlagErrorFunc(explainRemovedFlags)
	f := upgradeCmd.Flags()
	f.BoolVar(&upgradeFlags.Force, "force", false, "Reconfigure all settings")
	f.BoolVar(&upgradeFlags.RestartServices, "restart", false, "Automatically restart services after upgrade")
	f.BoolVar(&upgradeFlags.SkipChecks, "skip-checks", false, "Skip minimum resource checks (RAM/CPU)")
	f.StringVar(&upgradeFlags.Env, "env", "", "Target environment for remote rolling upgrade (devnet, testnet)")
	f.StringVar(&upgradeFlags.NodeFilter, "node", "", "Upgrade a single node IP only")
	f.BoolVar(&upgradeFlags.Yes, "yes", false, "Execute the rolling upgrade plan (without it the plan is printed and nothing is restarted)")
	f.IntVar(&upgradeFlags.Delay, "delay", int(rollout.GateBudget.Seconds()),
		"Seconds a node has to rejoin the cluster after its upgrade before the rollout stops")
	f.Bool("nameserver", false, "Make this node a nameserver (uses saved preference if not specified)")
	f.StringVar(&upgradeFlags.PublicIP, "public-ip", "",
		"This node's public IP, recorded as node.public_ip (default: the recorded one, else the source address of the default route)")

	// Set by the orchestrator when it re-execs itself after swapping the
	// binary; not something an operator ever passes.
	f.BoolVar(&upgradeFlags.ReexecedAfterBinarySwap, "reexeced-after-binary-swap", false, "")
	_ = f.MarkHidden("reexeced-after-binary-swap")

	// Accepted only from the post-swap re-exec; checkUpgradeAnyoneClient
	// refuses it from an operator.
	f.BoolVar(&upgradeAnyoneClient, upgradeReexecAnyoneClientFlag, false, "")
	_ = f.MarkHidden(upgradeReexecAnyoneClientFlag)
}
