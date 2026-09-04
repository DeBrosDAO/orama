// Package domain implements `orama domain`, which attaches custom domains to
// deployments.
//
// The gateway has had these endpoints all along; there was no command for them,
// so the documented way to put a domain on an app was a hand-rolled curl plus
// a manual dig to check the TXT record had propagated. For a platform whose
// promise is that deploying is one command, that was a hole in the middle of it.
package domain

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/DeBrosOfficial/network/pkg/cli/shared"
	"github.com/spf13/cobra"
)

// Cmd is the root `orama domain` command.
var Cmd = &cobra.Command{
	Use:   "domain",
	Short: "Attach custom domains to your apps",
	Long: `Add, verify, list and remove custom domains.

A domain is proved yours with a TXT record before it serves traffic. 'add'
prints the record to create, 'verify' checks it.`,
}

// Flag storage is per command. A single set of package-level variables would
// be shared by every subcommand that binds them, so `add --wait` and
// `verify --wait` would have whichever default was registered last.
var (
	addFlags struct {
		app    string
		json   bool
		verify bool
		wait   time.Duration
	}
	verifyFlags struct {
		json bool
		wait time.Duration
	}
	listFlags struct {
		app  string
		json bool
	}
	removeFlags struct {
		json bool
	}
)

// verifyPollInterval is how often `verify --wait` re-asks the gateway.
//
// DNS propagation is measured in minutes and the gateway resolves the record
// on every call, so a tighter loop only adds queries.
const verifyPollInterval = 10 * time.Second

func init() {
	add := &cobra.Command{
		Use:   "add <domain>",
		Short: "Attach a domain to an app",
		Long: `Register a domain against a deployment and print the TXT record that proves
you own it.

The domain does not serve traffic until 'orama domain verify' succeeds.`,
		Args: cobra.ExactArgs(1),
		RunE: runAdd,
	}
	add.Flags().StringVar(&addFlags.app, "app", "", "Deployment to attach the domain to [required]")
	add.Flags().BoolVar(&addFlags.json, "json", false, "Print the gateway's reply as JSON")
	add.Flags().BoolVar(&addFlags.verify, "verify", false, "Wait for the TXT record and verify in one step")
	add.Flags().DurationVar(&addFlags.wait, "wait", 5*time.Minute, "How long --verify waits for the record to propagate")
	_ = add.MarkFlagRequired("app")

	verify := &cobra.Command{
		Use:   "verify <domain>",
		Short: "Check the TXT record and activate the domain",
		Long: `Ask the gateway to resolve the domain's TXT record and, if it matches, start
serving the domain.

With --wait the check is repeated until the record appears, which is what a
freshly created DNS record needs.`,
		Args: cobra.ExactArgs(1),
		RunE: runVerify,
	}
	verify.Flags().BoolVar(&verifyFlags.json, "json", false, "Print the gateway's reply as JSON")
	verify.Flags().DurationVar(&verifyFlags.wait, "wait", 0, "Keep checking until the record appears, up to this long")

	list := &cobra.Command{
		Use:   "list",
		Short: "List your custom domains",
		Long:  `List every custom domain in the namespace, or only one app's with --app.`,
		Args:  cobra.NoArgs,
		RunE:  runList,
	}
	list.Flags().StringVar(&listFlags.app, "app", "", "Only this deployment's domains")
	list.Flags().BoolVar(&listFlags.json, "json", false, "Print the gateway's reply as JSON")

	remove := &cobra.Command{
		Use:   "remove <domain>",
		Short: "Detach a domain",
		Long:  `Remove a custom domain and the DNS record that pointed it at your app.`,
		Args:  cobra.ExactArgs(1),
		RunE:  runRemove,
	}
	remove.Flags().BoolVar(&removeFlags.json, "json", false, "Print the gateway's reply as JSON")

	Cmd.AddCommand(add, verify, list, remove)
}

// normalizeDomain matches what the gateway does to the value it stores, so the
// name printed here is the name the gateway knows it by.
func normalizeDomain(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	return strings.TrimSuffix(s, "/")
}

// addResponse is what the gateway answers to an add.
type addResponse struct {
	Domain            string `json:"domain"`
	DeploymentName    string `json:"deployment_name"`
	VerificationToken string `json:"verification_token"`
	Status            string `json:"status"`
}

func runAdd(cmd *cobra.Command, args []string) error {
	domain := normalizeDomain(args[0])

	raw, err := shared.Request("POST", "/v1/deployments/domains/add", map[string]string{
		"deployment_name": addFlags.app,
		"domain":          domain,
	})
	if err != nil {
		return err
	}
	if addFlags.json {
		return printJSON(raw)
	}

	var resp addResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return fmt.Errorf("parse gateway response: %w", err)
	}

	fmt.Printf("Added %s to %s.\n\n", resp.Domain, resp.DeploymentName)
	fmt.Printf("Create this DNS record to prove you own it:\n\n")
	fmt.Printf("  Type   TXT\n")
	fmt.Printf("  Name   _orama-verify.%s\n", resp.Domain)
	fmt.Printf("  Value  %s\n\n", resp.VerificationToken)

	if !addFlags.verify {
		fmt.Printf("Then run: orama domain verify %s --wait 5m\n", resp.Domain)
		return nil
	}

	fmt.Printf("Waiting for the record, up to %s...\n", addFlags.wait)
	return verifyDomain(resp.Domain, addFlags.wait, addFlags.json)
}

func runVerify(cmd *cobra.Command, args []string) error {
	return verifyDomain(normalizeDomain(args[0]), verifyFlags.wait, verifyFlags.json)
}

// retryVerify reports whether a failed verify is worth asking again.
//
// Only a 400 is. That is what the gateway answers while the TXT record is
// missing or does not match yet, which is the whole reason a wait exists. A 404
// means the domain was never added and no amount of waiting changes it; a 401
// means the credential is wrong. Retrying either would turn a clear error into
// a long silence ending in the same error.
func retryVerify(err error, timeLeft bool) bool {
	return timeLeft && shared.StatusOf(err) == 400
}

// verifyDomain asks the gateway to check the TXT record, retrying until wait
// elapses.
func verifyDomain(domain string, wait time.Duration, asJSON bool) error {
	deadline := time.Now().Add(wait)

	for attempt := 1; ; attempt++ {
		raw, err := shared.Request("POST", "/v1/deployments/domains/verify",
			map[string]string{"domain": domain})
		if err == nil {
			if asJSON {
				return printJSON(raw)
			}
			fmt.Printf("✓ %s verified and now serving.\n", domain)
			return nil
		}

		if !retryVerify(err, time.Now().Before(deadline)) {
			if wait > 0 && shared.StatusOf(err) == 400 {
				return fmt.Errorf("the TXT record for _orama-verify.%s did not appear within %s: %w",
					domain, wait, err)
			}
			return err
		}

		fmt.Printf("  attempt %d: not visible yet, retrying in %s\n", attempt, verifyPollInterval)
		time.Sleep(verifyPollInterval)
	}
}

// listResponse is what the gateway answers to a list.
type listResponse struct {
	Domains []struct {
		DeploymentName     string     `json:"deployment_name"`
		Domain             string     `json:"domain"`
		VerificationStatus string     `json:"verification_status"`
		CreatedAt          time.Time  `json:"created_at"`
		VerifiedAt         *time.Time `json:"verified_at"`
	} `json:"domains"`
}

func runList(cmd *cobra.Command, args []string) error {
	path := "/v1/deployments/domains/list"
	if listFlags.app != "" {
		path += "?deployment_name=" + url.QueryEscape(listFlags.app)
	}

	raw, err := shared.Request("GET", path, nil)
	if err != nil {
		return err
	}
	if listFlags.json {
		return printJSON(raw)
	}

	var resp listResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return fmt.Errorf("parse gateway response: %w", err)
	}
	if len(resp.Domains) == 0 {
		fmt.Println("No custom domains. Add one with: orama domain add <domain> --app <name>")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintf(w, "DOMAIN\tAPP\tSTATUS\tVERIFIED\n")
	for _, d := range resp.Domains {
		verified := "—"
		if d.VerifiedAt != nil {
			verified = d.VerifiedAt.Format(time.RFC3339)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", d.Domain, d.DeploymentName, d.VerificationStatus, verified)
	}
	if err := w.Flush(); err != nil {
		return fmt.Errorf("write domain table: %w", err)
	}
	return nil
}

func runRemove(cmd *cobra.Command, args []string) error {
	domain := normalizeDomain(args[0])

	raw, err := shared.Request("DELETE",
		"/v1/deployments/domains/remove?domain="+url.QueryEscape(domain), nil)
	if err != nil {
		return err
	}
	if removeFlags.json {
		return printJSON(raw)
	}
	fmt.Printf("✓ %s removed.\n", domain)
	return nil
}

// printJSON writes the gateway's reply verbatim, indented.
func printJSON(raw []byte) error {
	var pretty map[string]any
	if err := json.Unmarshal(raw, &pretty); err != nil {
		// Not an object: pass it through as it came.
		fmt.Println(strings.TrimSpace(string(raw)))
		return nil
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(pretty)
}
