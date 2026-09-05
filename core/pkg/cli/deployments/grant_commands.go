package deployments

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"text/tabwriter"

	"github.com/DeBrosOfficial/network/pkg/cli/printer"
	"github.com/DeBrosOfficial/network/pkg/cli/shared"
	"github.com/spf13/cobra"
)

// GrantsCmd is `orama app grants`.
//
// A deployed app used to run as whatever key somebody had pasted into its
// image, which is a namespace key: an application compromise was a namespace
// takeover, and nothing the app did was attributable to the app. It is a
// principal of its own now, and this is where you say what it may reach.
var GrantsCmd = &cobra.Command{
	Use:   "grants",
	Short: "Say what a deployed app may do, as itself",
	Long: `Read and change what a deployment is allowed to reach.

Your app is handed a short-lived token of its own at start, in the file named by
$ORAMA_TOKEN_FILE, and renews it with the gateway before it expires. It reaches
nothing until you grant it something — which is the point: an app that ships
with no credential cannot leak one.

A deployment cannot be granted the control plane. If something needs to deploy
or mint keys, that is a person or a CI key, not an app.`,
}

var grantResource string

func init() {
	list := &cobra.Command{
		Use:   "list [app]",
		Short: "Show what deployments in this namespace may do",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runGrantsList,
	}

	set := &cobra.Command{
		Use:   "set <app> <role>",
		Short: "Grant a deployment a role",
		Long: `Give a deployment a role in its own namespace.

  runtime  the data plane: invoke, storage, push, webrtc, proxy, pubsub, cache
  reader   nothing beyond the routes that ask for no grant

The change reaches a running app on its next token renewal, or immediately if
you redeploy.`,
		Example: `  orama app grants set my-api runtime
  orama app grants set my-api runtime --resource pubsub:topic=orders.*`,
		Args: cobra.ExactArgs(2),
		RunE: runGrantsSet,
	}
	set.Flags().StringVar(&grantResource, "resource", "",
		"Narrow the role to a resource, e.g. pubsub:topic=orders.*")

	GrantsCmd.AddCommand(list, set)
}

func runGrantsList(cmd *cobra.Command, args []string) error {
	path := "/v1/deployments/grants"
	if len(args) == 1 {
		path += "?name=" + url.QueryEscape(args[0])
	}
	raw, err := shared.Request("GET", path, nil)
	if err != nil {
		return err
	}
	if printer.For(cmd).JSONMode() {
		fmt.Println(string(raw))
		return nil
	}

	var resp struct {
		Namespace string `json:"namespace"`
		Grants    []struct {
			Deployment string `json:"deployment"`
			Role       string `json:"role"`
			Resource   string `json:"resource"`
		} `json:"grants"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return fmt.Errorf("parse gateway response: %w", err)
	}
	if len(resp.Grants) == 0 {
		fmt.Println("No deployment in this namespace has been granted anything.")
		fmt.Println("Its token reaches nothing until you run 'orama app grants set <app> runtime'.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintf(w, "APP\tROLE\tRESOURCE\n")
	for _, g := range resp.Grants {
		resource := g.Resource
		if resource == "" {
			resource = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", g.Deployment, g.Role, resource)
	}
	return w.Flush()
}

func runGrantsSet(cmd *cobra.Command, args []string) error {
	body := map[string]string{"name": args[0], "role": args[1]}
	if grantResource != "" {
		body["resource"] = grantResource
	}
	raw, err := shared.Request("POST", "/v1/deployments/grants", body)
	if err != nil {
		return err
	}
	if printer.For(cmd).JSONMode() {
		fmt.Println(string(raw))
		return nil
	}

	var resp struct {
		Deployment string `json:"deployment"`
		Role       string `json:"role"`
		Applies    string `json:"applies"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return fmt.Errorf("parse gateway response: %w", err)
	}
	fmt.Printf("%s may now act as '%s' in this namespace.\n", resp.Deployment, resp.Role)
	fmt.Printf("Takes effect %s.\n", resp.Applies)
	return nil
}
