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
		cli.HandleNamespaceDelete(forceFlag)
	},
}

var listCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List namespaces owned by the current wallet",
	Run: func(cmd *cobra.Command, args []string) {
		cli.HandleNamespaceList()
	},
}

var repairCmd = &cobra.Command{
	Use:   "repair <namespace>",
	Short: "Repair an under-provisioned namespace cluster",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		cli.HandleNamespaceRepair(args[0])
	},
}

var enableCmd = &cobra.Command{
	Use:   "enable <feature>",
	Short: "Enable a feature for a namespace",
	Long:  "Enable a feature for a namespace. Supported features: webrtc",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		ns, _ := cmd.Flags().GetString("namespace")
		cli.HandleNamespaceEnable(args[0], ns)
	},
}

var disableCmd = &cobra.Command{
	Use:   "disable <feature>",
	Short: "Disable a feature for a namespace",
	Long:  "Disable a feature for a namespace. Supported features: webrtc",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		ns, _ := cmd.Flags().GetString("namespace")
		cli.HandleNamespaceDisable(args[0], ns)
	},
}

var webrtcStatusCmd = &cobra.Command{
	Use:   "webrtc-status",
	Short: "Show WebRTC service status for a namespace",
	Run: func(cmd *cobra.Command, args []string) {
		ns, _ := cmd.Flags().GetString("namespace")
		cli.HandleNamespaceWebRTCStatus(ns)
	},
}

var keysCmd = &cobra.Command{
	Use:   "keys",
	Short: "Manage scoped API keys (bugboard #148)",
	Long:  "Create, list, and revoke scoped API keys. Profiles: invoke-only | app-runtime | admin.",
}

var keysCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Mint a new scoped API key",
	Run: func(cmd *cobra.Command, args []string) {
		scope, _ := cmd.Flags().GetString("scope")
		label, _ := cmd.Flags().GetString("label")
		ns, _ := cmd.Flags().GetString("namespace")
		cli.HandleNamespaceKeysCreate(ns, scope, label)
	},
}

var keysListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List scoped API keys",
	Run: func(cmd *cobra.Command, args []string) {
		ns, _ := cmd.Flags().GetString("namespace")
		cli.HandleNamespaceKeysList(ns)
	},
}

var keysRevokeCmd = &cobra.Command{
	Use:   "revoke",
	Short: "Revoke a single API key by id",
	Run: func(cmd *cobra.Command, args []string) {
		id, _ := cmd.Flags().GetInt("id")
		ns, _ := cmd.Flags().GetString("namespace")
		cli.HandleNamespaceKeysRevoke(ns, id)
	},
}

var keysRevokeLegacyCmd = &cobra.Command{
	Use:   "revoke-legacy",
	Short: "Revoke ALL legacy (unscoped) keys — the cutover step",
	Run: func(cmd *cobra.Command, args []string) {
		force, _ := cmd.Flags().GetBool("force")
		ns, _ := cmd.Flags().GetString("namespace")
		cli.HandleNamespaceKeysRevokeLegacy(ns, force)
	},
}

func init() {
	deleteCmd.Flags().Bool("force", false, "Skip confirmation prompt")
	enableCmd.Flags().String("namespace", "", "Namespace name")
	disableCmd.Flags().String("namespace", "", "Namespace name")
	webrtcStatusCmd.Flags().String("namespace", "", "Namespace name")

	keysCreateCmd.Flags().String("scope", "", "Profile (invoke-only|app-runtime|admin) or grant list")
	keysCreateCmd.Flags().String("label", "", "Human label for the key")
	keysCreateCmd.Flags().String("namespace", "", "Namespace name")
	keysListCmd.Flags().String("namespace", "", "Namespace name")
	keysRevokeCmd.Flags().Int("id", 0, "Key id to revoke")
	keysRevokeCmd.Flags().String("namespace", "", "Namespace name")
	keysRevokeLegacyCmd.Flags().Bool("force", false, "Skip confirmation prompt")
	keysRevokeLegacyCmd.Flags().String("namespace", "", "Namespace name")
	keysCmd.AddCommand(keysCreateCmd)
	keysCmd.AddCommand(keysListCmd)
	keysCmd.AddCommand(keysRevokeCmd)
	keysCmd.AddCommand(keysRevokeLegacyCmd)

	Cmd.AddCommand(listCmd)
	Cmd.AddCommand(deleteCmd)
	Cmd.AddCommand(repairCmd)
	Cmd.AddCommand(enableCmd)
	Cmd.AddCommand(disableCmd)
	Cmd.AddCommand(webrtcStatusCmd)
	Cmd.AddCommand(keysCmd)
}
