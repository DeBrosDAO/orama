package authcmd

import (
	"github.com/DeBrosOfficial/network/pkg/cli"
	"github.com/spf13/cobra"
)

// Cmd is the root command for authentication.
var Cmd = &cobra.Command{
	Use:   "auth",
	Short: "Authentication management",
	Long: `Manage authentication with the Orama network.

Signing in is a wallet signature over a gateway challenge. On a machine with
RootWallet running it is signed here; on one without — a server reached over
SSH, a container, CI — 'orama auth login' prints a code and 'orama auth approve'
on a machine that does have a wallet approves it.

What is stored is a session, not a key: an access token lasting 15 minutes,
renewed transparently from a refresh token.`,
}

var loginNamespace string

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Sign in, here or from another machine",
	RunE: func(cmd *cobra.Command, args []string) error {
		return cli.AuthLogin(loginNamespace)
	},
}

var (
	approveNamespace string
	approveDeny      bool
)

var approveCmd = &cobra.Command{
	Use:   "approve <code>",
	Short: "Approve a login waiting on another machine",
	Long: `Approve the code 'orama auth login' printed on a machine with no wallet on it.

It costs the same wallet signature signing in does, which is what makes the code
on its own worthless. --deny refuses instead, so the waiting machine stops
rather than polling until the code expires.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return cli.AuthApprove(args[0], approveNamespace, approveDeny)
	},
}

var logoutAll bool

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "End this session on the gateway and clear it here",
	RunE: func(cmd *cobra.Command, args []string) error {
		return cli.AuthLogout(logoutAll)
	},
}

var whoamiCmd = &cobra.Command{
	Use:   "whoami",
	Short: "Ask the gateway who this credential is and what it may do",
	RunE: func(cmd *cobra.Command, args []string) error {
		return cli.AuthWhoami()
	},
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show what is stored on this machine, without asking the gateway",
	RunE: func(cmd *cobra.Command, args []string) error {
		return cli.AuthStatus()
	},
}

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all stored credentials",
	RunE: func(cmd *cobra.Command, args []string) error {
		return cli.AuthList()
	},
}

var switchCmd = &cobra.Command{
	Use:   "switch",
	Short: "Switch between stored credentials",
	RunE: func(cmd *cobra.Command, args []string) error {
		return cli.AuthSwitch()
	},
}

var sessionsCmd = &cobra.Command{
	Use:   "sessions",
	Short: "Which machines are signed in as this wallet",
	RunE: func(cmd *cobra.Command, args []string) error {
		return cli.AuthSessionsList()
	},
}

var sessionsRevokeAll bool

var sessionsRevokeCmd = &cobra.Command{
	Use:   "revoke [id]",
	Short: "End one session, or every one",
	Long: `End a session listed by 'orama auth sessions'.

Ending a session stops it minting new access tokens. One already minted keeps
working until it expires, at most 15 minutes.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var id int64
		if len(args) == 1 {
			parsed, err := parseSessionID(args[0])
			if err != nil {
				return err
			}
			id = parsed
		}
		return cli.AuthSessionsRevoke(id, sessionsRevokeAll)
	},
}

func init() {
	loginCmd.Flags().StringVar(&loginNamespace, "namespace", "", "Namespace name")

	approveCmd.Flags().StringVar(&approveNamespace, "namespace", "",
		"Namespace to sign in to (defaults to the one this machine is signed in to)")
	approveCmd.Flags().BoolVar(&approveDeny, "deny", false, "Refuse the login instead of approving it")

	logoutCmd.Flags().BoolVar(&logoutAll, "all", false,
		"End every session for this wallet, not only this machine's")

	sessionsRevokeCmd.Flags().BoolVar(&sessionsRevokeAll, "all", false, "End every session for this wallet")
	sessionsCmd.AddCommand(sessionsRevokeCmd)

	Cmd.AddCommand(loginCmd)
	Cmd.AddCommand(approveCmd)
	Cmd.AddCommand(logoutCmd)
	Cmd.AddCommand(whoamiCmd)
	Cmd.AddCommand(statusCmd)
	Cmd.AddCommand(listCmd)
	Cmd.AddCommand(switchCmd)
	Cmd.AddCommand(sessionsCmd)
}
