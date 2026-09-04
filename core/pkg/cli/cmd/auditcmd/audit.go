package auditcmd

import (
	"strings"

	"github.com/DeBrosOfficial/network/pkg/cli"
	"github.com/DeBrosOfficial/network/pkg/cli/printer"
	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/spf13/cobra"
)

// Cmd reads a namespace's audit trail.
var Cmd = &cobra.Command{
	Use:   "audit",
	Short: "Read this namespace's audit trail",
	Long: `Print what has happened in a namespace: sign-ins, keys minted and revoked,
grants given and taken away, deployments, functions, secrets and namespace changes.

Events are shown oldest first. --follow keeps the command running and prints new
ones as they are recorded.

Actions: ` + strings.Join(auth.AuditActions, ", "),
	RunE: func(cmd *cobra.Command, args []string) error {
		ns, _ := cmd.Flags().GetString("namespace")
		action, _ := cmd.Flags().GetString("action")
		principal, _ := cmd.Flags().GetString("principal")
		since, _ := cmd.Flags().GetString("since")
		limit, _ := cmd.Flags().GetInt("limit")
		follow, _ := cmd.Flags().GetBool("follow")
		return cli.AuditList(printer.For(cmd), cli.AuditFilter{
			Namespace: ns,
			Action:    action,
			Principal: principal,
			Since:     since,
			Limit:     limit,
			Follow:    follow,
		})
	},
}

func init() {
	Cmd.Flags().String("namespace", "", "Namespace name")
	Cmd.Flags().String("action", "", "Show only this action")
	Cmd.Flags().String("principal", "", "Show only what this wallet or key did")
	Cmd.Flags().String("since", "", "Show only what happened after this time (RFC3339, or the created_at of a row)")
	Cmd.Flags().Int("limit", 0, "How many events to fetch at once (default 50, max 200)")
	Cmd.Flags().BoolP("follow", "f", false, "Keep running and print new events as they are recorded")
}
