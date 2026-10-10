package node

import (
	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/trust"
	"github.com/spf13/cobra"
)

var trustCmd = &cobra.Command{
	Use:   "trust",
	Short: "Manage what this node accepts code from, besides its operator's wallet",
}

var trustAddRoot trust.AddRootOptions

var trustAddRootCmd = &cobra.Command{
	Use:   "add-root <root.json>",
	Short: "Adopt a TUF release root on this node (requires sudo)",
	Long: `Adopt a TUF release root as /etc/orama/release-root.json.

A node trusts its operator's wallet by default (/etc/orama/archive-signers). A
cluster may also trust a release root: a set of keys whose threshold signature
on release metadata makes an archive installable without the operator building
and signing it. The Orama release root is one; a cluster adopts it by choice
and can drop it by deleting the file.

The root is checked before it is written: well-formed, signed by its own keys at
its threshold, not expired. Adopting a root other than the one already adopted
needs --replace. This command changes this node only; 'orama maint build
--release-root' puts the root in a signed archive, and every node that installs
that archive adopts it.

--rotate adopts the root as the next version of the one adopted: it has to be
signed by the adopted root's keys at their threshold and by its own, exactly as a
client following the release repository checks a rotation. A push of a release
uses it, so a root the operator renewed or rotated reaches this node without
--replace and without anyone's word for it.

Examples:
  sudo orama node trust add-root ./root.json
  sudo orama node trust add-root --rotate ./2.root.json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		trustAddRoot.File = args[0]
		return trust.AddRoot(trustAddRoot, cmd.OutOrStdout())
	},
}

func init() {
	trustAddRootCmd.Flags().BoolVar(&trustAddRoot.Replace, "replace", false, "Replace a different release root that is already adopted")
	trustAddRootCmd.Flags().BoolVar(&trustAddRoot.Rotate, "rotate", false, "Adopt the root as the next version of the adopted one, verified against it (a rotation)")
	trustCmd.AddCommand(trustAddRootCmd)
	Cmd.AddCommand(trustCmd)
}
