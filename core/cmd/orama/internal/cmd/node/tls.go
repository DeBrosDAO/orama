package node

import (
	"github.com/DeBrosOfficial/network/cmd/orama/internal/printer"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/tlsstatus"
	"github.com/spf13/cobra"
)

var tlsCmd = &cobra.Command{
	Use:   "tls",
	Short: "This node's TLS certificates",
}

var tlsStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show whether this node holds its certificates, and why not (requires sudo)",
	Long: `Report the certificate of every name this node's Caddy manages: the base
domain and its wildcard, and any site of its own.

For each name it says whether Caddy serves an issued certificate, who issued it
and when it expires. When it does not, it says what the certificate authority
last answered to the order, read from Caddy's log: the ACME problem type (for
example rateLimited), the CA's message and, when the CA gave one, the time it
will accept a new order. A rate limit shows here and nowhere a client can see:
the client's only symptom is "tls: internal error".

Reads the journal of orama-namespace-caddy@index, so it needs root. --json
prints the same as a document with a "names" list.

See orama.network/docs/operator/tls-certificates.`,
	Example: `  sudo orama node tls status
  sudo orama node tls status --json`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return tlsstatus.Handle(printer.For(cmd))
	},
}

func init() {
	tlsCmd.AddCommand(tlsStatusCmd)
}
