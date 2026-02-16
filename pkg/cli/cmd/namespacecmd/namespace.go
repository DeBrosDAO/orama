package namespacecmd

import (
	"github.com/DeBrosOfficial/network/pkg/cli"
	"github.com/spf13/cobra"
)

// Cmd is the root command for namespace management.
var Cmd = &cobra.Command{
	Use:     "namespace",
	Aliases: []string{"ns"},
	Short:   "Manage namespaces",
	Long:    `List, delete, and repair namespaces on the Orama network.`,
}

var deleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Delete the current namespace and all its resources",
	Run: func(cmd *cobra.Command, args []string) {
		forceFlag, _ := cmd.Flags().GetBool("force")
		var cliArgs []string
		cliArgs = append(cliArgs, "delete")
		if forceFlag {
			cliArgs = append(cliArgs, "--force")
		}
		cli.HandleNamespaceCommand(cliArgs)
	},
}

var repairCmd = &cobra.Command{
	Use:   "repair <namespace>",
	Short: "Repair an under-provisioned namespace cluster",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		cli.HandleNamespaceCommand(append([]string{"repair"}, args...))
	},
}

func init() {
	deleteCmd.Flags().Bool("force", false, "Skip confirmation prompt")

	Cmd.AddCommand(deleteCmd)
	Cmd.AddCommand(repairCmd)
}
