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

var listCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List namespaces owned by the current wallet",
	Run: func(cmd *cobra.Command, args []string) {
		cli.HandleNamespaceCommand([]string{"list"})
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

var enableCmd = &cobra.Command{
	Use:   "enable <feature>",
	Short: "Enable a feature for a namespace",
	Long:  "Enable a feature for a namespace. Supported features: webrtc",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		ns, _ := cmd.Flags().GetString("namespace")
		cliArgs := []string{"enable", args[0]}
		if ns != "" {
			cliArgs = append(cliArgs, "--namespace", ns)
		}
		cli.HandleNamespaceCommand(cliArgs)
	},
}

var disableCmd = &cobra.Command{
	Use:   "disable <feature>",
	Short: "Disable a feature for a namespace",
	Long:  "Disable a feature for a namespace. Supported features: webrtc",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		ns, _ := cmd.Flags().GetString("namespace")
		cliArgs := []string{"disable", args[0]}
		if ns != "" {
			cliArgs = append(cliArgs, "--namespace", ns)
		}
		cli.HandleNamespaceCommand(cliArgs)
	},
}

var webrtcStatusCmd = &cobra.Command{
	Use:   "webrtc-status",
	Short: "Show WebRTC service status for a namespace",
	Run: func(cmd *cobra.Command, args []string) {
		ns, _ := cmd.Flags().GetString("namespace")
		cliArgs := []string{"webrtc-status"}
		if ns != "" {
			cliArgs = append(cliArgs, "--namespace", ns)
		}
		cli.HandleNamespaceCommand(cliArgs)
	},
}

func init() {
	deleteCmd.Flags().Bool("force", false, "Skip confirmation prompt")
	enableCmd.Flags().String("namespace", "", "Namespace name")
	disableCmd.Flags().String("namespace", "", "Namespace name")
	webrtcStatusCmd.Flags().String("namespace", "", "Namespace name")

	Cmd.AddCommand(listCmd)
	Cmd.AddCommand(deleteCmd)
	Cmd.AddCommand(repairCmd)
	Cmd.AddCommand(enableCmd)
	Cmd.AddCommand(disableCmd)
	Cmd.AddCommand(webrtcStatusCmd)
}
