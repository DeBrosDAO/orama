package node

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	cli "github.com/DeBrosOfficial/network/cmd/orama/internal"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/printer"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/dnsdelegation"
	"github.com/spf13/cobra"
)

var dnsCmd = &cobra.Command{
	Use:   "dns",
	Short: "Cluster DNS: what the outside world needs to reach its nameservers",
}

var (
	dnsDelegationEnv       string
	dnsCloudflareTokenFile string
)

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

After the records it asks DNS whether the parent zone returns them, and says
which NS or glue record is missing or points at another address. The answer is
stored on the environment (environments.json), replacing the last result for
the domain. A resolver that cannot answer is reported and nothing is
stored. --json adds "delegated" and "findings" to each domain.

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
	dnsDelegationCmd.Flags().StringVar(&dnsCloudflareTokenFile, "cloudflare-token-file", "", "Create or update the NS and glue records in the parent Cloudflare zone, then check DNS")
	dnsCmd.AddCommand(dnsDelegationCmd)
}

// dnsCheckTimeout bounds the DNS questions of one delegation check.
const dnsCheckTimeout = 30 * time.Second

// recordDelegation stores a check result on the environment; tests replace it.
var recordDelegation = cli.RecordDelegation

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
	if !out.JSONMode() {
		for _, d := range delegations {
			out.Printf("; %s — create these in the parent zone %s\n", d.Domain, parentZoneLabel(d.Domain))
			out.Printf("%s\n\n", strings.Join(dnsdelegation.Records(d), "\n"))
		}
	}
	if dnsCloudflareTokenFile != "" {
		if err := applyCloudflare(out, delegations); err != nil {
			return err
		}
	}
	return checkAndRecord(out, env, delegations, dnsdelegation.Lookups{})
}

func applyCloudflare(out *printer.Printer, delegations []dnsdelegation.Delegation) error {
	token, err := os.ReadFile(dnsCloudflareTokenFile)
	if err != nil {
		return clierr.Failure("read the Cloudflare token: %v", err)
	}
	cf := &dnsdelegation.Cloudflare{Token: strings.TrimSpace(string(token))}
	for _, d := range delegations {
		if err := cf.Apply(context.Background(), d); err != nil {
			return clierr.Failure("%v", err)
		}
		out.Printf("Cloudflare zone updated and DNS matches for %s\n", d.Domain)
	}
	return nil
}

// checkAndRecord asks DNS whether each domain is delegated, prints the answer
// and stores it on the environment. A domain DNS could not be asked about is
// reported and not stored: nothing was learned about it.
func checkAndRecord(out *printer.Printer, env string, delegations []dnsdelegation.Delegation, l dnsdelegation.Lookups) error {
	ctx, cancel := context.WithTimeout(context.Background(), dnsCheckTimeout)
	defer cancel()
	reports := dnsdelegation.CheckAll(ctx, delegations, l)
	if out.JSONMode() {
		if err := out.JSON(reports); err != nil {
			return err
		}
	} else {
		printReports(out, reports)
	}
	for _, r := range reports {
		if r.CheckError != "" {
			continue
		}
		status := cli.DelegationStatus{Domain: r.Domain, Delegated: r.Delegated}
		for _, f := range r.Findings {
			status.Findings = append(status.Findings, f.String())
		}
		if err := recordDelegation(env, status); err != nil {
			return clierr.Failure("store the delegation check for %s: %v", r.Domain, err)
		}
	}
	return nil
}

func printReports(out *printer.Printer, reports []dnsdelegation.Report) {
	for _, r := range reports {
		switch {
		case r.CheckError != "":
			out.Warn("%s: could not check DNS, so the delegation is unverified: %s", r.Domain, r.CheckError)
		case r.Delegated:
			out.Ok("%s: DNS returns every record above", r.Domain)
		default:
			out.Warn("%s: not delegated yet", r.Domain)
			for _, f := range r.Findings {
				out.Printf("    %s\n", f)
			}
		}
	}
}

// parentZoneLabel names where the records go, for the operator.
func parentZoneLabel(domain string) string {
	parent := dnsdelegation.ParentZone(domain)
	if !strings.Contains(parent, ".") {
		return fmt.Sprintf(".%s: at your registrar, as custom nameservers with glue (host) records", parent)
	}
	return parent + ": as records in that zone, wherever its DNS is hosted"
}
