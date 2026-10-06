package namespacecmd

import (
	"strings"

	cli "github.com/DeBrosOfficial/network/cmd/orama/internal"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/printer"
	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
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
	RunE: func(cmd *cobra.Command, args []string) error {
		forceFlag, _ := cmd.Flags().GetBool("force")
		return cli.NamespaceDelete(forceFlag)
	},
}

var listCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List namespaces owned by the current wallet",
	RunE: func(cmd *cobra.Command, args []string) error {
		return cli.NamespaceList(printer.For(cmd))
	},
}

var repairCmd = &cobra.Command{
	Use:   "repair <namespace>",
	Short: "Repair an under-provisioned namespace cluster",
	Long: "Repair an under-provisioned namespace cluster. Run it on a node. " +
		"It talks to that node's gateway on the node's WireGuard address; " +
		"localhost is where public traffic arrives, so a repair sent there is refused.",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return cli.NamespaceRepair(args[0])
	},
}

var enableCmd = &cobra.Command{
	Use:   "enable <feature>",
	Short: "Enable a feature for a namespace",
	Long:  "Enable a feature for a namespace. Supported features: webrtc",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ns, _ := cmd.Flags().GetString("namespace")
		return cli.NamespaceEnable(args[0], ns)
	},
}

var disableCmd = &cobra.Command{
	Use:   "disable <feature>",
	Short: "Disable a feature for a namespace",
	Long:  "Disable a feature for a namespace. Supported features: webrtc",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ns, _ := cmd.Flags().GetString("namespace")
		return cli.NamespaceDisable(args[0], ns)
	},
}

var webrtcStatusCmd = &cobra.Command{
	Use:   "webrtc-status",
	Short: "Show WebRTC service status for a namespace",
	RunE: func(cmd *cobra.Command, args []string) error {
		ns, _ := cmd.Flags().GetString("namespace")
		return cli.NamespaceWebRTCStatus(ns)
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
	RunE: func(cmd *cobra.Command, args []string) error {
		scope, _ := cmd.Flags().GetString("scope")
		label, _ := cmd.Flags().GetString("label")
		ns, _ := cmd.Flags().GetString("namespace")
		days, _ := cmd.Flags().GetInt("expires-in-days")
		return cli.NamespaceKeysCreate(ns, scope, label, days)
	},
}

var keysListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List scoped API keys",
	RunE: func(cmd *cobra.Command, args []string) error {
		ns, _ := cmd.Flags().GetString("namespace")
		return cli.NamespaceKeysList(ns)
	},
}

var keysRevokeCmd = &cobra.Command{
	Use:   "revoke",
	Short: "Revoke a single API key by id",
	RunE: func(cmd *cobra.Command, args []string) error {
		id, _ := cmd.Flags().GetInt("id")
		ns, _ := cmd.Flags().GetString("namespace")
		return cli.NamespaceKeysRevoke(ns, id)
	},
}

var sessionPolicyCmd = &cobra.Command{
	Use:   "session-policy",
	Short: "Show or set who may sign in to a namespace and what its sessions bind",
	Long: `With no flags, show the namespace's session policy. With flags, set them; a flag
left out keeps its value.

  --sign-in members   only wallets holding a grant sign in (the default)
  --sign-in open      a wallet holding none may sign in too, as an end user of
                      the application. It gets a session and no API key, is
                      never granted anything, and reaches only what a grantless
                      wallet reaches. Closing it again ends those sessions at
                      their next refresh. A namespace nobody owns stays closed.

  --device-policy     optional | required | approval: what an end user's
                      sign-in must bind. Requiring devices revokes the sign-in
                      keys end users already hold.

Changing either needs write access to the namespace.

  orama namespace session-policy --namespace myapp --sign-in open`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ns, _ := cmd.Flags().GetString("namespace")
		signIn, _ := cmd.Flags().GetString("sign-in")
		device, _ := cmd.Flags().GetString("device-policy")
		return cli.NamespaceSessionPolicy(printer.For(cmd), ns, signIn, device)
	},
}

var createCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a namespace and start its cluster",
	Long: `Create a namespace. The wallet you are signed in as becomes its owner.

Creating a namespace used to happen by itself: signing in to a name that did
not exist created it. So a typo made a namespace, and one belonged to whoever
happened to sign in first.

  orama namespace create myapp
  orama auth login --namespace myapp`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return cli.NamespaceCreate(args[0])
	},
}

var keysRotateCmd = &cobra.Command{
	Use:   "rotate",
	Short: "Mint a successor to a key and keep the old one working for an overlap",
	Long: `Mint a new key with the same grants and label, and shorten the original's life
to the overlap.

Rotating by minting a new key and revoking the old one in the same breath is an
outage: whatever is deployed with the old key stops the moment the new one
exists. The overlap is the window in which to deploy the successor — both keys
work, and the original then expires on its own.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		id, _ := cmd.Flags().GetInt("id")
		overlap, _ := cmd.Flags().GetInt("overlap-days")
		expires, _ := cmd.Flags().GetInt("expires-in-days")
		ns, _ := cmd.Flags().GetString("namespace")
		return cli.NamespaceKeysRotate(ns, id, overlap, expires)
	},
}

var keysRevokeLegacyCmd = &cobra.Command{
	Use:   "revoke-legacy",
	Short: "Revoke ALL legacy (unscoped) keys — the cutover step",
	RunE: func(cmd *cobra.Command, args []string) error {
		force, _ := cmd.Flags().GetBool("force")
		ns, _ := cmd.Flags().GetString("namespace")
		return cli.NamespaceKeysRevokeLegacy(ns, force)
	},
}

func init() {
	deleteCmd.Flags().Bool("force", false, "Skip confirmation prompt")
	enableCmd.Flags().String("namespace", "", "Namespace name")
	disableCmd.Flags().String("namespace", "", "Namespace name")
	webrtcStatusCmd.Flags().String("namespace", "", "Namespace name")
	sessionPolicyCmd.Flags().String("namespace", "", "Namespace name")
	sessionPolicyCmd.Flags().String("sign-in", "", "Who may sign in: members | open")
	sessionPolicyCmd.Flags().String("device-policy", "", "What an end user's sign-in must bind: optional | required | approval")

	// The grant list comes from the gateway rather than a hand-written string,
	// which is how the help came to advertise grants the validator refused.
	keysCreateCmd.Flags().String("scope", "",
		"Profile (invoke-only|app-runtime|admin) or a comma-separated grant list ("+
			strings.Join(auth.AllGrants(), ", ")+")")
	keysCreateCmd.Flags().String("label", "", "Human label for the key")
	keysCreateCmd.Flags().String("namespace", "", "Namespace name")
	keysListCmd.Flags().String("namespace", "", "Namespace name")
	keysRevokeCmd.Flags().Int("id", 0, "Key id to revoke")
	keysRevokeCmd.Flags().String("namespace", "", "Namespace name")
	keysRevokeLegacyCmd.Flags().Bool("force", false, "Skip confirmation prompt")
	keysRevokeLegacyCmd.Flags().String("namespace", "", "Namespace name")
	keysCreateCmd.Flags().Int("expires-in-days", 0,
		"How long the key lives, in days (default 90, max 365). A key that never expires is not on offer")
	keysRotateCmd.Flags().Int("id", 0, "Key id to rotate")
	keysRotateCmd.Flags().Int("overlap-days", 0,
		"How long the old key keeps working (default 7, max 30) — the window to deploy the new one")
	keysRotateCmd.Flags().Int("expires-in-days", 0, "How long the successor lives, in days (default 90)")
	keysRotateCmd.Flags().String("namespace", "", "Namespace name")

	keysCmd.AddCommand(keysCreateCmd)
	keysCmd.AddCommand(keysRotateCmd)
	keysCmd.AddCommand(keysListCmd)
	keysCmd.AddCommand(keysRevokeCmd)
	keysCmd.AddCommand(keysRevokeLegacyCmd)

	Cmd.AddCommand(createCmd)
	Cmd.AddCommand(listCmd)
	Cmd.AddCommand(deleteCmd)
	Cmd.AddCommand(repairCmd)
	Cmd.AddCommand(enableCmd)
	Cmd.AddCommand(disableCmd)
	Cmd.AddCommand(webrtcStatusCmd)
	Cmd.AddCommand(sessionPolicyCmd)
	Cmd.AddCommand(keysCmd)
	Cmd.AddCommand(backupSealCmd)
	Cmd.AddCommand(backupOpenCmd)
}
