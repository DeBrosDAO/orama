// Package networkcmd is `orama network`: the networks the CLI knows and which
// one it talks to. It replaces `orama env`, which stays for one release as a
// hidden alias of the same commands.
package networkcmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	cli "github.com/DeBrosOfficial/network/cmd/orama/internal"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/printer"
	"github.com/DeBrosOfficial/network/pkg/netregistry"
)

// envDeprecation is what `orama env` prints before it runs.
const envDeprecation = "orama env is deprecated and goes away in the next release: use `orama network` (same subcommands)"

// httpClient builds the client `network add <url>` fetches with. Tests replace it.
var httpClient = netregistry.NewHTTPClient

// Cmd is `orama network`.
var Cmd = newGroup("network", "Choose the network the CLI talks to", `List, choose, add and remove the networks the CLI knows.

A network is a name for something you can reach: a cluster, through its
gateway, and, for a network of the registry such as stagenet, the chain it runs,
the seeds to join through and the release root its software is verified against.
Every other command talks to the active network, or to the one --env names.

  orama network list                          every network and where it comes from
  orama network use <name>                    make one active
  orama network add <name> <gateway-url>      reach a cluster by its gateway
  orama network add <manifest-url>            trust a network that publishes a manifest
  orama network current                       the active network
  orama network remove <name>                 forget one`)

// EnvCmd is `orama env`: the commands of `orama network` under their old name,
// hidden, printing a notice. It is removed in the release after this one.
var EnvCmd = deprecatedEnv()

func deprecatedEnv() *cobra.Command {
	cmd := newGroup("env", "Deprecated: use orama network", "Deprecated: `orama env` is `orama network`.")
	cmd.Hidden = true
	cmd.PersistentPreRun = func(c *cobra.Command, _ []string) {
		fmt.Fprintln(c.ErrOrStderr(), envDeprecation)
	}
	return cmd
}

func newGroup(use, short, long string) *cobra.Command {
	cmd := &cobra.Command{Use: use, Short: short, Long: long}
	cmd.AddCommand(newListCmd(), newCurrentCmd(), newUseCmd(), newAddCmd(), newRemoveCmd())
	return cmd
}

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List every network and where it comes from",
		Long: `List the networks of the registry (built into this binary or added by URL) and the
gateways you configured, one row each. A name that is both shows both. The active
network is marked with *.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cli.NetworkList(printer.For(cmd))
		},
	}
}

func newCurrentCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "current",
		Short: "Show the active network",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cli.NetworkCurrent(printer.For(cmd))
		},
	}
}

func newUseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "use <name>",
		Short: "Make a network the active one",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return cli.NetworkUse(printer.For(cmd), args[0])
		},
	}
}

func newRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Forget a network",
		Long: `Forget the network of that name: the one you added by URL and the gateway you
configured, whichever exist. A network built into this binary stays in the list.
A name that is neither is not an error.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return cli.NetworkRemove(printer.For(cmd), args[0])
		},
	}
}

// looksLikeURL reports whether arg is written as an http(s) URL, which is what
// separates a manifest URL from a network name.
func looksLikeURL(arg string) bool {
	return strings.HasPrefix(arg, "https://") || strings.HasPrefix(arg, "http://")
}

func newAddCmd() *cobra.Command {
	var (
		caFile  string
		network string
		yes     bool
	)
	cmd := &cobra.Command{
		Use:   "add <manifest-url> | <name> <gateway-url> [description]",
		Short: "Add a network by its manifest, or a cluster by its gateway",
		Long: `Add a network, one of two ways.

With one argument, a manifest URL: https://<host>/<path>/manifest.json. The
manifest names the chain id, the genesis digest, the seeds, the release channel
and the digest of the release root the network's software is verified against.
The command fetches it (https only, size-bounded) and the release-root.json
beside it, checks the root against the digest, shows the chain id and the digest,
and asks you to type yes. Nothing is stored before that. --yes confirms for a
script; the digest is then the only thing you trust, so a script should pass the
URL of a manifest it already checked.

With a name and a gateway URL, a cluster you reach through that gateway (and an
optional description). The URL must be https:// with a host (http:// only for a
gateway on this machine: localhost or a loopback address), because every command
sends its credential there. --ca-file trusts a PEM bundle for this gateway's
domain and every name under it, in addition to the system roots: a cluster on
Let's Encrypt's staging CA, or on a private CA. It is not trusted for any other
host. --network records which registry network the cluster runs on.`,
		Args: cobra.RangeArgs(1, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printer.For(cmd)
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			if len(args) == 1 {
				if !looksLikeURL(args[0]) {
					return clierr.Usage("%q is neither a manifest URL (https://<host>/<path>/manifest.json) nor a name followed by a gateway URL", args[0])
				}
				if caFile != "" || network != "" {
					return clierr.Usage("--ca-file and --network describe a gateway: give a name and a gateway URL")
				}
				return cli.NetworkAddManifest(ctx, p, httpClient(), cmd.InOrStdin(), args[0], yes)
			}
			if yes {
				return clierr.Usage("--yes confirms a manifest: give one manifest URL")
			}
			return cli.NetworkAddCluster(p, args, cli.ClusterOptions{CAFile: caFile, Network: network})
		},
	}
	cmd.Flags().StringVar(&caFile, "ca-file", "", "PEM CA bundle to trust for this gateway's domain only")
	cmd.Flags().StringVar(&network, "network", "", "Registry network this cluster runs on")
	cmd.Flags().BoolVar(&yes, "yes", false, "Trust the manifest's network without asking")
	return cmd
}
