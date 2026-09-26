package node

import (
	"fmt"
	"strings"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/printer"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/dnsdelegation"
	"github.com/spf13/cobra"
)

var dnsCmd = &cobra.Command{
	Use:   "dns",
	Short: "Cluster DNS: what the outside world needs to reach its nameservers",
}

var dnsDelegationEnv string

var dnsDelegationCmd = &cobra.Command{
	Use:   "delegation",
	Short: "Print the NS and glue records to create at the parent zone",
	Long: `Print exactly the records the operator must create at the parent zone (or
registrar) so the internet reaches this cluster's nameservers: one NS record
per nameserver, and the glue A record that gives each nameserver its address.

Nameserver slots (ns1, ns2, …) are claimed by the --nameserver nodes as they
come up, so which address holds which name is only known to the cluster. This
reads it from the cluster over SSH. Only slots whose glue the cluster has
written are listed — the same set the cluster's own zone publishes.

Run it again after adding or removing a nameserver, and update the parent
zone to match. See docs/NAMESERVER_SETUP.md.`,
	Example: `  orama node dns delegation --env devnet
  orama node dns delegation --env devnet --json`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runDNSDelegation(printer.For(cmd), dnsDelegationEnv)
	},
}

func init() {
	dnsDelegationCmd.Flags().StringVar(&dnsDelegationEnv, "env", "", "Environment to read (devnet, testnet, …) [required]")
	dnsCmd.AddCommand(dnsDelegationCmd)
}

func runDNSDelegation(out *printer.Printer, env string) error {
	if env == "" {
		return clierr.Usage("--env is required\nUsage: orama node dns delegation --env <environment>")
	}
	delegations, err := dnsdelegation.Read(env)
	if err != nil {
		return clierr.Unavailable("%v", err)
	}
	if len(delegations) == 0 {
		return clierr.NotFound("no nameserver in %s has claimed a slot yet: install at least one node with --nameserver "+
			"and wait for its first DNS sweep (30s after it registers)", env)
	}
	if out.JSONMode() {
		return out.JSON(delegations)
	}
	for _, d := range delegations {
		out.Printf("; %s — create these in the parent zone %s\n", d.Domain, parentZoneLabel(d.Domain))
		out.Printf("%s\n\n", strings.Join(dnsdelegation.Records(d), "\n"))
	}
	return nil
}

// parentZoneLabel names where the records go, for the operator.
func parentZoneLabel(domain string) string {
	parent := dnsdelegation.ParentZone(domain)
	if !strings.Contains(parent, ".") {
		return fmt.Sprintf(".%s: at your registrar, as custom nameservers with glue (host) records", parent)
	}
	return parent + ": as records in that zone, wherever its DNS is hosted"
}
